# Spec 0010 — Menu IA: exportar prompt, importar palavras-chave e categorias (entrega E9)

**Data:** 21/09/2026 · **Fase:** 4 (núcleo financeiro) · **Entrega:** E9 (nova)
**ADRs relacionados:** ADR-013 (sem FK física), ADR-016 (transferência como par), ADR-017b (árvore de
exatamente 2 níveis), ADR-024 (importação em duas fases), ADR-026 (classificação por palavras-chave é
determinística, sem IA, em Go), ADR-028 (reprocessamento de transferências já gravadas), ADR-029
(naturezas de categoria), ADR-033 (semente de palavras-chave).
**ADR proposto:** o **próximo número livre** de `docs/ARQUITETURA.md`, lido no instante da escrita —
"integração com IA é **fora do processo**: o produto nunca fala com IA, ele produz texto e consome
JSON" (o `arquiteto` registra).

> **Numeração:** 0007 foi abandonada (virou a emenda §12 da spec 0004) e 0009 está citada
> nominalmente em `docs/ROADMAP.md` para a E6; 0010 é o primeiro número que não colide com nenhuma
> das duas.

> Esta spec é **normativa**. Onde ela e a documentação geral divergirem, vale a documentação geral
> (`AGENTS.md` e `docs/SEGURANCA.md`), e o desvio deve ser reportado, não implementado em silêncio.

---

## 1. Problema

Cadastrar palavra-chave é trabalho manual e repetitivo: a pessoa olha "MERCADO DO SEU JOSE" no extrato,
decide que é `Alimentação > Mercado` e digita a palavra à mão, uma por uma, por categoria — e às vezes
descobre no meio do caminho que a categoria certa nem existe ainda. Uma IA externa faz esse mapeamento
bem, mas só se receber o contexto certo, e só se a resposta dela puder voltar para dentro do app sem
quebrar nada.

## 2. Escopo

### 2.1 Entra

- **Menu "IA"** na navegação, com a tela `/ia` em três seções: **Exportar**, **Importar** e
  **Reprocessar**.
- **Janela de trabalho** — um único seletor no topo da tela, em **meses civis de competência**
  (`fromMonth`/`toMonth`, **no máximo 3**, padrão = mês corrente e os dois anteriores). Ele vale para
  as três seções: é o período exportado, o período medido na prévia e o período reprocessado. Um
  conceito só, sem três datas diferentes se contradizendo — e em mês, não em data, porque as rotas de
  reprocessamento só falam mês (emenda §10, achado A2).
- **Exportar** — o servidor devolve um **prompt pronto em Markdown** com o contexto da aplicação, as
  regras do motor de correspondência, as contas e suas palavras-chave, as categorias e suas
  palavras-chave, e as movimentações do período **agrupadas por descrição**. A tela oferece **Copiar**
  e **Baixar .md**.
- **Importar** — cola o JSON que a IA devolveu → **prévia obrigatória** → **Confirmar**. O JSON pode:
  - **adicionar palavras-chave** a categorias e contas que já existem;
  - **criar subcategorias** (e o grupo delas, quando não existir) já com as suas palavras-chave.
- **Prévia com seleção** — cada categoria nova vem **marcada**, em bloco visualmente separado, e pode
  ser **desmarcada** uma a uma. Cada palavra-chave de **conta** vem com o **impacto medido**: quantos
  lançamentos do período ela passa a tornar candidatos a transferência.
- **Reprocessar** — um botão que roda, **nesta ordem**, a detecção de transferências e a
  categorização automática, mês a mês, usando as rotas que já existem. Nunca automático.

### 2.2 Fica explicitamente de fora

- **Qualquer chamada de rede para IA.** O backend não tem cliente de IA, não tem chave de API, não tem
  endpoint que fale com provedor nenhum. O transporte é a pessoa: ela copia, cola, copia de volta.
- **Criar, renomear, arquivar ou excluir CONTA.** Conta tem saldo de abertura, instituição e dia de
  fechamento — é objeto do mundo real, nasce pela mão da pessoa. O JSON só adiciona palavra-chave a
  conta existente.
- **Renomear, mover, arquivar ou excluir categoria**, e **criar grupo solto** (ver §4.3).
- **Remover** palavra-chave pelo import. A operação é aditiva; tirar palavra continua no diálogo.
- **Importar lançamentos, valores ou saldos** vindos do JSON. Nenhum centavo entra por aqui.
- **Reprocessar sozinho** ao confirmar o import, e aprender sem clique.
- Exportação em CSV (Fase 5) e importação por upload de arquivo (v1 é colar texto).

## 3. Comportamento — Exportar

1. A pessoa abre `/ia`. A janela de trabalho já vem preenchida com o mês corrente e os dois anteriores.
2. Validação: `fromMonth ≤ toMonth`; a janela tem no máximo **3 meses de competência**, inclusive;
   mês malformado, ausente ou repetido é 400. Janela longa demais é **400 `VALIDATION_FAILED`** em
   `fields.toMonth` — a tela barra antes (ver a emenda §10, achados A2 e A3).
3. O servidor monta o prompt (§3.1) e devolve **o texto pronto**, mais as contagens para a tela mostrar
   ("32 descrições distintas · 4 contas · 41 categorias").
4. A tela exibe o prompt num painel somente-leitura, com **Copiar** e **Baixar .md**
   (`homefinance-prompt-2026-07-a-2026-09.md`).
5. **Aviso obrigatório e visível** acima do painel: este texto contém descrições e valores das suas
   movimentações; ao colar numa IA de terceiros, você está enviando esses dados para fora. O aplicativo
   não envia nada sozinho.
6. Período sem movimentação gera prompt válido mesmo assim (contas e categorias bastam), com uma linha
   dizendo que não houve movimentação.

### 3.1 O que o prompt contém (normativo — a ordem é esta)

1. **Papel e contexto** — o que é o HomeFinance, o que é uma palavra-chave, e que a resposta será lida
   por um programa, não por uma pessoa.
2. **Como o motor casa** — as três regras do `internal/textmatch` (exata = 100; aproximação por
   substring comum de 5 runas ou mais; erro de digitação a distância 1), o **limiar 80**, o empate que
   anula a sugestão, e as consequências práticas: palavra com menos de 5 runas só casa inteira; palavra
   genérica casa com o que não devia.
3. **A diferença entre palavra-chave de categoria e de conta** — e é a parte que mais decide a
   qualidade do resultado:
   - **de categoria** é o que identifica o *estabelecimento ou o assunto* do gasto (`zaffari`,
     `drogaria`);
   - **de conta** é o que aparece na descrição quando o dinheiro **sai ou entra daquela conta vista do
     extrato de OUTRA conta** (`nubank`, `nu pagamentos`) — ela é o gatilho de **transferência
     interna**, e uma palavra genérica aqui (`pagamento`, `transferencia`, `pix`) converte dezenas de
     lançamentos legítimos em transferência. O prompt diz isso com todas as letras e manda preferir o
     nome próprio da instituição.
4. **Regras que o JSON precisa respeitar** — 2 a 40 runas; letras, dígitos, espaço e os símbolos
   permitidos; máximo **20 palavras por item** (contando as que já existem); a mesma palavra **não
   pode** estar em duas categorias nem em duas contas (pode estar numa categoria *e* numa conta); não
   repetir palavra que o item já tem.
5. **Quando criar categoria nova** — regra explícita e restritiva: **use sempre uma categoria
   existente quando ela servir**; só proponha categoria nova quando nenhuma das existentes couber, e
   nunca proponha uma categoria para a qual você não tenha ao menos uma movimentação real do período.
   Categoria nova é sempre uma **subcategoria** dentro de um grupo (existente ou novo).
6. **Contas** — `id`, nome, tipo e palavras-chave atuais de cada conta **ativa**.
7. **Categorias** — `id`, caminho `Grupo > Folha`, natureza (`expense`/`income`/`investment`/
   `redemption`) e palavras-chave atuais de cada categoria **ativa**. A lista de grupos vem destacada,
   porque é nela que uma subcategoria nova se encaixa.
8. **Movimentações do período, agrupadas por descrição normalizada** — uma linha por descrição
   distinta: descrição exibível, ocorrências, total em reais, tipo (`kindGroup`: receita · despesa ·
   transferência · investimento), contas em que apareceu e categoria atual (o caminho quando todas as
   ocorrências têm a mesma; `várias` quando divergem; `—` quando não tem). Ordem: ocorrências
   decrescente.
9. **Tarefa e formato de saída** — o schema do JSON (§4.1), as proibições, e **um exemplo completo de
   resposta válida**.

**Teto de linhas — MEDIDO em 21/09/2026, e o resultado está aqui.** O corpus real foi medido antes de
qualquer número ser cravado: **78 descrições distintas** em 3 meses no banco real (7,7 KB de tabela no
prompt), e **264** numa casa pesada realista de 750 lançamentos (56 KB). Razão de expansão medida:
1,06 no real, 1,17–1,35 no sintético.

`maxDescriptionsInPrompt = 500`. Ele **não corta nada** em nenhum dos dois casos medidos — e é por isso
que existe: ele não é um corte de conteúdo, é a **única defesa do tamanho da resposta** (nenhum
middleware do projeto limita corpo de resposta). Havendo corte numa casa maior, ele é por ocorrências
decrescentes, o prompt **declara em texto** quantas ficaram de fora, e `stats.truncatedDescriptions`
diz o mesmo número — a tela não é a única a saber. Dado colateral da medição, que explica a forma da
cauda: **73% das descrições aparecem uma única vez**, então cortar por frequência é barato em
lançamentos e caro em variedade.

Isto emenda a redação original ("só existe teto se o corpus passar disso"), que confundia dois tetos
diferentes: o de **conteúdo** (que a medição dispensou) e o de **tamanho de resposta** (que continua
necessário). O teto da agregação no banco é um terceiro, muito maior (`MaxDescriptionGroupRows = 5000`,
tudo-ou-nada com 422).

**Minimização (normativo) — e a correção de 21/09/2026, que é a parte que importa.** A minimização é
**por campo**, e a redação original desta seção ("o prompt não contém nome ou e-mail de ninguém") era
**falsa**, como o `revisor-seguranca` mostrou. Ela vale para os campos estruturados e **não** vale para
o texto livre:

- **Não vão** (campo a campo): id da casa, id de lançamento, saldo de conta, instituição, agência,
  número de conta, dias de fechamento e vencimento de fatura, e o nome e o e-mail de cadastro das
  pessoas da casa. Da conta saem **quatro** campos — id, nome, tipo, palavras-chave — e nada mais.
- **Vai**, e é o que domina o texto: a **descrição do lançamento, como ela aparece no app**. O
  sanitizador da importação remove o que identifica terceiros por documento (CPF, CNPJ, agência,
  conta), mas **mantém de propósito o nome da contraparte** — é ele que responde "quem eu paguei"
  (`internal/importer/sanitize/sanitize.go`). Então descrição leva nome de terceiro (`Pix enviado -
  Fulano de Tal`) e leva a **mensagem do Pix**, escrita por quem pagou, onde cabe e-mail e telefone.
- **Vai** também a taxonomia inteira, inclusive categorias sem movimento no período — e nomes de
  categoria podem ser dado sensível ("Terapia", "Medicamentos"). É decisão consciente: o prompt precisa
  da taxonomia para mandar a IA reusar o que já existe em vez de inventar categoria.

Os ids de conta e de categoria vão porque são a chave de volta. São inúteis fora desta casa, mas
**não são opacos**: são UUIDv7 e carregam o instante de criação. A palavra "opaco" não deve virar carga
estrutural em decisão futura.

**Consequência para o aviso da tela (§3.5):** como a mitigação inteira desta ameaça é consentimento
informado, o aviso precisa dizer que **a descrição vai como está e costuma trazer o nome de quem pagou
ou recebeu**. Um aviso que nega a maior categoria de dado pessoal que de fato sai — e que é dado de
**terceiros**, não só da casa — torna o consentimento não informado, e é pior que aviso nenhum.

## 4. Comportamento — Importar

1. A pessoa cola o JSON e clica **Conferir**.
2. `POST /ai/keyword-import/preview` roda **todas** as validações **sem escrever nada** e devolve o
   relatório: o que entra, o que é pulado, o que é recusado — cada recusa com motivo.
3. A tela mostra três blocos, nesta ordem:
   - **Estrutura nova** — as categorias a criar, cada uma **marcada** e desmarcável, com o caminho
     (`Alimentação > Padaria`, `Saúde (grupo novo) > Farmácia`) e as palavras que nascem com ela;
   - **Palavras-chave de conta** — cada uma com o **impacto medido** (§4.4);
   - **Palavras-chave de categoria** — agrupadas por categoria.
   No topo, os totais ("3 categorias novas · 18 palavras entram · 6 já existiam · 3 recusadas").
   **Confirmar** fica desabilitado quando não sobra nada para aplicar.
4. **Confirmar** chama `POST /ai/keyword-import/confirm` com o **mesmo JSON** mais a lista do que foi
   desmarcado. O servidor **revalida tudo do zero** — a prévia não é credencial, e o estado pode ter
   mudado no meio — e grava numa **única transação** (`UnitOfWork`), na ordem: grupos novos →
   subcategorias novas → palavras-chave.
5. O relatório final é o que **de fato** entrou. Havendo o que reprocessar, a tela leva à seção
   **Reprocessar** (§5) — sem rodar nada sozinha.
6. JSON inválido, vazio ou que não é JSON: 400 com mensagem útil, **sem eco do conteúdo colado**.

### 4.1 Formato do JSON (normativo)

```json
{
  "homefinanceKeywordImport": 1,
  "newCategories": [
    {
      "group": "Alimentação",
      "name": "Padaria",
      "kind": "expense",
      "add": ["padaria", "panificadora"]
    }
  ],
  "categoryKeywords": [
    {
      "categoryId": "018f...",
      "categoryPath": "Alimentação > Mercado",
      "add": ["mercado do seu jose", "zaffari"]
    }
  ],
  "accountKeywords": [
    {
      "accountId": "018f...",
      "accountName": "Nubank",
      "add": ["nu pagamentos"]
    }
  ],
  "notes": "texto livre — aceito e descartado"
}
```

- `homefinanceKeywordImport` — versão do formato. Ausente ou diferente de `1` é 400.
- `categoryPath` / `accountName` são **conferência obrigatória**, não decoração: se o nome não bater
  (comparado normalizado) com o item daquele `id`, a entrada inteira é **recusada**. É o que pega id
  alucinado, id trocado entre linhas e JSON gerado do export de outra casa.
- `newCategories[]` — a categoria nova **não tem id** (ainda não existe): a chave é o par
  `group` + `name`. `kind` só é usado quando o **grupo** é novo; com grupo existente a natureza é
  **herdada** e um `kind` divergente é **recusa**, não silêncio.
- `add` — só adiciona. Não existe campo de remoção nesta spec.
- `notes` — aceito para a IA ter onde despejar a explicação que sempre quer dar. É **descartado**:
  nunca gravado, nunca exibido. Qualquer **outro** campo desconhecido é 400.
- As três listas são opcionais; todas ausentes ou vazias é **400 `VALIDATION_FAILED`** em
  `fields.payload` (emenda §10, achado A3).

**Envelope das duas rotas** (o corpo que o frontend manda):

```json
{
  "payload": { "...o JSON da IA, como veio..." },
  "fromMonth": "2026-07",
  "toMonth": "2026-09",
  "skipNewCategories": ["alimentacao > padaria"]
}
```

`fromMonth`/`toMonth` são a janela de trabalho, em **competência**, e servem para medir o impacto
(§4.4). No confirm são validados e não usados — exigi-los nas duas rotas mantém os corpos idênticos,
que é o que torna literal a promessa "o confirm revalida tudo do zero".
`skipNewCategories` são os caminhos **normalizados** das categorias desmarcadas — ausente no
preview, preenchido no confirm conforme a pessoa desmarcou. O caminho normalizado é um `ref` estável
entre prévia e confirmação, sem estado no servidor.

### 4.2 Validações de palavra-chave (normativo — a ordem é esta)

| # | Verificação | Resultado |
|---|---|---|
| 1 | Corpo de até **128 KB**; JSON bem formado; versão `1`; sem campo desconhecido | 400 na rota |
| 2 | Até **200 entradas** por lista; até **20 palavras** por entrada | 400 na rota |
| 3 | `id` existe **na casa do token** (nunca do cliente) e não está excluído | recusa `ITEM_NOT_FOUND` |
| 4 | Item não está **arquivado** (arquivado não participa da correspondência) | recusa `ITEM_ARCHIVED` |
| 5 | `categoryPath`/`accountName` bate com o item do `id` | recusa `NAME_MISMATCH` |
| 5b | O `categoryId` **não** é um grupo com subcategoria ativa (spec 0005 §12 — grupo com filha não recebe lançamento, logo não recebe palavra). Linha acrescentada pelo achado A6 | recusa `group_has_children` |
| 6 | Palavra válida: 2–40 runas normalizada, charset, e sobra palavra útil depois da tokenização | recusa `INVALID_KEYWORD` |
| 7 | Palavra **já existe no próprio item** | **pula**, `ALREADY_PRESENT` — é o que torna reimportar o mesmo JSON inofensivo |
| 8 | Palavra **já existe em outro item do mesmo tipo** | recusa `KEYWORD_TAKEN`, dizendo de quem é |
| 9 | Mesma palavra em **dois itens diferentes do próprio JSON** | recusa **as duas**, `AMBIGUOUS_IN_PAYLOAD` — ambíguo é pior que vazio |
| 10 | Mesma palavra **repetida no mesmo item** | deduplica em silêncio |
| 11 | Total do item (existentes + novas) de até 20 | recusa as excedentes na ordem do JSON, `LIMIT_EXCEEDED` |

### 4.3 Validações de categoria nova (normativo)

A árvore tem **exatamente 2 níveis** (ADR-017b), e o import respeita uma restrição a mais que a tela:
**toda categoria criada aqui é uma folha**. O grupo nasce só como continente, e **nunca recebe
palavra-chave** — grupo com subcategoria ativa não pode ter palavras (spec 0005 §12), então dar
palavras a um grupo que acabou de ganhar uma folha seria criar o problema no ato da criação. Quem quer
grupo com palavras próprias cria na tela de categorias, conscientemente.

| # | Verificação | Resultado |
|---|---|---|
| 1 | `group` e `name` válidos (1–60 caracteres, como no `POST /categories`) | recusa `INVALID_NAME` |
| 2 | Grupo **existe e está ativo** → a folha entra nele, natureza **herdada**; `kind` divergente do grupo | recusa `KIND_MISMATCH` |
| 3 | Grupo **não existe** → será criado, e `kind` passa a ser **obrigatório** e restrito ao conjunto fechado | recusa `KIND_REQUIRED` / `INVALID_KIND` |
| 4 | `group > name` **já existe ativo** | não é criação: vira `MERGED_INTO_EXISTING` e as palavras vão para a categoria existente, passando por todas as validações da §4.2 |
| 5 | Existe categoria **arquivada** com o mesmo nome no mesmo pai | recusa `NAME_TAKEN_ARCHIVED`, orientando desarquivar — nunca cria uma segunda com o mesmo nome |
| 6 | Apontar para um grupo que na verdade é subcategoria | recusa `PARENT_NOT_GROUP` (não há nível 3) |
| 7 | Criações fariam a casa passar do **teto de 200 categorias** | recusa as excedentes, `HOUSEHOLD_LIMIT`, na ordem do JSON |
| 8 | Dois blocos com o **mesmo** `group > name` no próprio JSON | o primeiro vale, os demais `DUPLICATE_IN_PAYLOAD` |
| 9 | Caminho está em `skipNewCategories` | `SKIPPED_BY_USER` — a categoria não nasce e **as palavras dela somem junto** |

Não há teto de criações por lote além do teto de 200 da casa: o freio é o prompt (§3.1, item 5), que
manda preferir o que já existe, e a prévia, onde tudo vem à vista e desmarcável. A tela **destaca a
contagem** quando o lote propõe muita estrutura nova.

### 4.4 Impacto medido da palavra-chave de conta (normativo)

Para cada palavra-chave de **conta** que entraria, a prévia informa **quantos lançamentos vivos
`income`/`expense` da janela de trabalho passariam a ser candidatos a transferência** por causa dela —
rodando o `internal/textmatch` que já existe, sobre as descrições do período, sem escrever nada.

É o estrago **medido** antes de acontecer: `pagamento` aparecendo como "87 lançamentos" salta aos
olhos; `nu pagamentos` como "4" passa batido, e deve mesmo. A tela destaca visualmente as palavras
acima de um limiar de atenção, **sem bloquear** — quem decide é a pessoa, mas ela decide com o número
na frente. Palavra-chave de **categoria** não recebe essa medição: categorizar errado se desfaz numa
linha, converter transferência errada mexe em duas.

Regra transversal das §§4.2–4.4: **uma entrada recusada nunca derruba o lote**. O que é válido entra, o
que não é volta no relatório com o motivo.

## 5. Comportamento — Reprocessar (o item 2 do pedido)

Palavra-chave nova não mexe, sozinha, em nenhum lançamento já gravado: ela só passa a valer quando o
reprocessamento roda. As duas rotas que fazem isso **já existem** e já têm prévia, transação,
auditoria, rate limit próprio e 409 de conflito — esta spec **não cria rota nova de reprocessamento**,
ela orquestra as existentes na ordem certa:

1. `POST /transfers/detect` — converte pares `income`/`expense` em `transfer_out`/`transfer_in`.
2. `POST /transactions/auto-categorize` — categoriza os que estão **sem** categoria.

### 5.1 A ordem é obrigatória, e é esta

**Transferências primeiro, categorização depois.** `POST /transfers/detect` **zera o `categoryId` das
duas pernas** ao converter o par (ADR-028). Categorizar antes seria escrever categoria em linhas que a
etapa seguinte esvazia: trabalho perdido, e um relatório que mente ("42 categorizados" quando 6 foram
zerados em seguida). É também a mesma precedência que a análise da importação já aplica — **conta
batendo vence categoria batendo** (spec 0005 §4.2.1).

### 5.2 Fluxo

1. A seção **Reprocessar** usa a janela de trabalho e mostra os **meses civis** que ela cobre, tratados
   como meses de **competência** ("jul, ago, set" — no máximo 3).
2. **Conferir** roda a prévia (`dryRun: true`) das duas etapas, em todos os meses, e mostra o
   consolidado: "3 pares de transferência · 42 lançamentos categorizados · 12 seguem sem categoria".
   As candidatas sem par aparecem com o motivo (`no_mirror`), porque a orientação é importar o extrato
   da outra conta — o sistema **nunca inventa a outra perna**.
3. **Reprocessar** executa mês a mês, cada mês na ordem da §5.1. Cada chamada é a rota existente, com
   sua própria transação e seu próprio registro de auditoria.
4. **409 `CONFLICT` em qualquer ponto para tudo**: a tela mostra o que já foi aplicado (os meses
   anteriores ficam feitos — cada rota é uma transação), diz que o estado mudou e pede uma prévia nova.
   Nada de continuar às cegas depois de um conflito.
5. Ao fim, o resumo do que mudou, com link para `/transferencias` e `/lancamentos` para conferir.
6. **Idempotente:** rodar de novo sem nada novo converte 0 e categoriza 0 — as duas rotas já garantem
   isso.

### 5.3 O que continua verdadeiro depois de reprocessar

Herdado das rotas existentes, e que esta spec **não pode quebrar** — vira critério de aceite:

- O **saldo das contas não muda** ao converter um par: valor, data, competência, descrição, origem,
  lote e chave de deduplicação ficam como estão; só `kind` e `categoryId` mudam.
- **Reimportar o mesmo extrato continua caindo em `duplicado_exato`** — a chave de deduplicação é
  preservada na conversão.
- **Despesa ligada a fatura de cartão não é reprocessada**, nem como candidata nem como espelho: o
  total cobrado da fatura é o número que a pessoa confere contra o banco.
- **Transferência não tem categoria**, e o auto-categorize não toca em `transfer_in`/`transfer_out`.
- **Nenhuma perna é inventada**: candidata sem espelho fica como está.

## 6. Contrato (rascunho para o `arquiteto` refinar no OpenAPI)

| Rota | Descrição |
|---|---|
| `GET /api/v1/ai/export-prompt?fromMonth=&toMonth=` | `{ prompt, fromMonth, toMonth, generatedAt, stats: { accounts, categories, descriptions, transactions, truncatedDescriptions } }`. Os dois são `YearMonth` obrigatórios, lidos com `SoleQueryValue` — paga a dívida de HPP nesta rota desde o nascimento |
| `POST /api/v1/ai/keyword-import/preview` | Envelope da §4.1. Devolve `KeywordImportReport`. **Não escreve** |
| `POST /api/v1/ai/keyword-import/confirm` | Mesmo envelope, com `skipNewCategories`. Revalida do zero, grava em uma transação, devolve `KeywordImportReport` |

Nenhuma rota nova de reprocessamento: a tela usa `POST /transfers/detect` e
`POST /transactions/auto-categorize`, que já existem.

`KeywordImportReport`:
`{ totals: { categoriesCreated, added, skipped, rejected }, newCategories: [ { ref, group, name, kind, groupIsNew, outcome, add: [...] } ], items: [ { type: "category"|"account", id, name, added: [...], skipped: [{keyword, reason}], rejected: [{keyword, reason, detail}], impact: { transferCandidates } } ] }`.
Os `reason`/`outcome` são os conjuntos **fechados** das §§4.2–4.3; o texto em português é do frontend,
não da API. `impact` só vem em item de conta, e só no preview.

## 7. Dados (rascunho para o `arquiteto-dados`)

**Nenhuma tabela nova, nenhuma coluna nova, nenhuma migração.** O schema segue **v4**. A exportação é
leitura pura (agregação `GROUP BY description_norm` sobre `transactions`, com `household_id` do token);
a importação escreve em `categories`, `category_keywords` e `account_keywords`, que já existem — a
criação de categoria usa o mesmo caminho do `POST /categories` (mesma validação, mesmo teto de 200,
mesmo `NameTaken`, mesma auditoria), não um atalho paralelo.

Dois pontos para o `arquiteto-dados` confirmar antes da implementação:

1. **`NameTaken` e categoria arquivada** — hoje ele enxerga só as ativas (comentário em
   `category/seed.go`, 18/09/2026). A §4.3(5) exige distinguir "nome livre" de "nome de uma arquivada";
   se o repositório ainda não sabe responder isso, é ele que muda, não a regra.
2. **`description_norm` não tem índice próprio** e agrupar 3 meses por ela é caso de uso novo. Plano
   verificado com `EXPLAIN` antes de cravar qualquer coisa; índice só entra se a medição pedir.

## 8. Segurança (o que esta feature muda no modelo de ameaças)

1. **Saída de dados para terceiro, por ato consciente.** É a única novidade real de ameaça. O
   aplicativo não envia nada: quem copia é a pessoa. Mitigação = o aviso da §3.5 e a minimização da
   §3.1 — que é minimização **por campo** (sem saldo, sem instituição, sem dados bancários, sem id de
   casa nem de lançamento, sem nome ou e-mail de cadastro), e **não** cobre o texto livre: a descrição
   vai como está e costuma trazer nome de terceiro e a mensagem do Pix. É por isso que o aviso da §3.5
   tem de dizer isso em vez de negá-lo — ver a correção na §3.1.
2. **O JSON é entrada hostil.** Veio de uma IA, que erra e inventa, e o texto pode ter passado por
   qualquer lugar. Limite de bytes, limite de itens, `additionalProperties: false`, conjunto fechado de
   campos, zero interpolação em SQL, zero reflexão do conteúdo colado nas mensagens de erro.
3. **Escrita estrutural vinda de fora é a novidade desta versão da spec.** Criar categoria muda a
   taxonomia da casa. Contenção: só folha, nunca grupo solto; teto de 200 respeitado; tudo à vista e
   desmarcável na prévia; e **nada destrutivo existe no formato** — não há renomear, mover, arquivar
   nem excluir. O pior caso de um JSON malicioso é categoria a mais, que se arquiva em um clique.
4. **`notes` descartado** fecha a superfície de texto livre de IA renderizado dentro do app.
5. **BOLA (risco nº 1).** Todo `id` do JSON é resolvido **com `household_id` do token**. Id de outra
   casa é indistinguível de id inexistente: `ITEM_NOT_FOUND`. O grupo de uma categoria nova também é
   procurado só dentro da casa.
6. **Rate limit** nas duas rotas novas (`internal/platform/config/ratelimits.go`), por casa: a
   exportação é agregação sobre 3 meses e a prévia roda o motor de correspondência. O reprocessamento
   consome os baldes que já existem (60/h cada) — 3 meses × (prévia + execução) = 6 chamadas por balde,
   folgado.
7. **Auditoria.** Categoria criada gera `category.created`; palavra-chave usa os eventos existentes
   `category.updated` / `account.updated`, com origem marcando que veio do import de IA. As palavras
   **não** vão para o log (spec 0005 §4.1). O reprocessamento mantém a auditoria própria das rotas.
8. **Nenhuma dependência nova**, nem no backend nem no frontend.
9. **Injeção de prompt pelo conteúdo do extrato é inerente e NÃO é neutralizável no servidor.** A
   descrição de um lançamento entra literal na tabela da seção 8, e ela vem de arquivo de banco **e do
   campo de mensagem de um PIX**, que um terceiro escolhe. Um lançamento com "IGNORE AS INSTRUÇÕES
   ACIMA" chega à IA, e não há como impedir isso sem deturpar a descrição que a pessoa vê no próprio
   app. O que a feature garante é o **enquadramento**, travado por teste: a descrição fica dentro de
   uma célula, numa linha só, com `|` e `\` escapados e caracteres de controle neutralizados — ela não
   vira título, não abre cerca de código e não parte a tabela. A contenção real não está no prompt:
   está em a volta ser um **formato fechado** que só sabe adicionar palavra-chave e criar subcategoria,
   sob prévia obrigatória. Nenhum teste deve sugerir imunidade; os que existem afirmam exatamente o
   enquadramento, e nada além dele. (Acrescentado em 21/09/2026 a partir do achado G do `qa-testes`.)

## 9. Critérios de aceite

**Exportação**

1. `fromMonth`/`toMonth` cobrindo exatamente 3 meses é aceito; **um mês a mais** é 400
   `VALIDATION_FAILED` em `fields.toMonth`.
2. `toMonth < fromMonth` é 400; mês malformado é 400; parâmetro repetido
   (`?fromMonth=a&fromMonth=b`) é 400.
3. O prompt contém as 9 seções da §3.1, nesta ordem, e o exemplo de JSON de saída.
4. O prompt explica a diferença entre palavra-chave de categoria e de conta, e alerta contra palavra
   genérica de conta (§3.1, item 3) — verificado por presença no texto gerado.
5. Duas descrições que só diferem em acento e caixa aparecem como **uma** linha agrupada, com a soma
   das ocorrências e dos centavos.
6. Conta e categoria **arquivadas** não aparecem no prompt.
7. O prompt **não** contém: id da casa, e-mail, nome de usuário, saldo de conta, id de lançamento
   (teste por varredura do texto gerado).
8. Lançamento de **outra casa** nunca aparece no prompt (BOLA, com duas casas povoadas).
9. Período sem movimentação gera prompt válido, com a linha de "nenhuma movimentação".
10. O total em centavos de um grupo bate com a soma dos lançamentos daquele grupo (teste cruzado).

**Importação — palavras-chave**

11. JSON válido com 2 palavras novas: a prévia mostra 2 em `added` e **nada é gravado** (verificado
    relendo o banco); o confirm grava as 2.
12. **Reimportar o mesmo JSON**: 0 adicionadas, 2 `ALREADY_PRESENT`, banco inalterado.
13. Palavra que já pertence a outra categoria: `KEYWORD_TAKEN` com o nome do dono; as demais do lote
    entram normalmente.
14. Mesma palavra em duas categorias do mesmo JSON: **as duas** recusadas, `AMBIGUOUS_IN_PAYLOAD`.
15. Item com **18** palavras recebendo 3: entram as 2 primeiras, a 3ª é `limit_exceeded`. E o caso
    complementar: item com **19** recebendo 3 — entra 1, as outras 2 são `limit_exceeded`. (O texto
    original desta linha dizia "19 → entram 2", que estoura o teto de 20; corrigido pelo achado A5.)
16. `categoryId` certo com `categoryPath` errado: `NAME_MISMATCH`, nada gravado para aquele item.
17. `categoryId` de **outra casa**: `ITEM_NOT_FOUND`, indistinguível de id inexistente.
18. Categoria arquivada: `ITEM_ARCHIVED`.
19. Palavra com 1 runa, com 41 runas, com marcação HTML, com fragmento de SQL, ou só de palavras
    vazias ("de ltda"): `INVALID_KEYWORD`, e o banco segue íntegro.
20. Corpo de 200 KB é **413 `PAYLOAD_TOO_LARGE`** antes de qualquer parsing de negócio — é o que o
    `MaxBytesReader` já faz nas 43 rotas do projeto (achado A4); 201 entradas numa lista é 400.
21. Campo desconhecido no raiz é 400; `notes` com 10 KB é aceito e **não** aparece em lugar nenhum.
22. Falha no meio do lote não deixa nada pela metade: a transação é uma só (erro forçado).
23. A confirmação gera evento de auditoria por item alterado, **sem** as palavras no log.

**Importação — categorias novas**

24. Grupo existente + folha nova: a folha nasce com a natureza **herdada** do grupo, e um `kind`
    divergente no JSON é `KIND_MISMATCH` (não silêncio).
25. Grupo novo sem `kind`: `KIND_REQUIRED`; `kind` fora do conjunto fechado: `INVALID_KIND`.
26. Grupo novo + folha: os dois nascem na **mesma transação**, e o **grupo não recebe palavra-chave**
    nenhuma (verificado no banco).
27. `group > name` que já existe ativo: `MERGED_INTO_EXISTING`, as palavras entram na categoria
    existente e nenhuma categoria é criada.
28. Nome igual ao de uma categoria **arquivada** no mesmo pai: `NAME_TAKEN_ARCHIVED`, nada criado.
29. Casa com 199 categorias recebendo 3 novas: entra 1, as outras 2 são `HOUSEHOLD_LIMIT`.
30. Categoria desmarcada na prévia (`skipNewCategories`): não nasce, e **as palavras dela também não
    entram** em lugar nenhum (`SKIPPED_BY_USER`).
31. Dois blocos com o mesmo `group > name`: o primeiro vale, o segundo é `DUPLICATE_IN_PAYLOAD`.
32. Categoria criada gera `category.created` na auditoria.
33. O formato **não tem** campo capaz de renomear, mover, arquivar ou excluir categoria — tentativa de
    incluir um é 400 por campo desconhecido.

**Impacto medido**

34. Palavra de conta genérica que casa com muitas descrições do período volta na prévia com a contagem
    correta em `impact.transferCandidates` (comparada com a contagem obtida rodando o matcher direto).
35. A medição **não escreve nada** e não aparece no confirm.
36. Palavra de **categoria** não traz `impact`.

**Reprocessamento**

37. A prévia consolidada bate, número a número, com a soma das prévias individuais de
    `/transfers/detect` e `/transactions/auto-categorize` de cada mês.
38. A execução chama as rotas na ordem **transferências → categorização**, em cada mês (verificado por
    ordem de chamadas, e por estado final).
39. Um lançamento que é par de transferência **e** teria categoria por palavra-chave termina como
    `transfer_out`/`transfer_in` com `categoryId` **nulo** — e não categorizado.
40. **Saldo das duas contas não muda** ao converter um par (comparado antes e depois).
41. Reimportar o mesmo extrato depois do reprocessamento continua caindo em `duplicado_exato`.
42. Despesa ligada a fatura de cartão **não** é convertida, e o total da fatura não muda.
43. Rodar o reprocessamento duas vezes seguidas: a segunda converte 0 e categoriza 0.
44. 409 no segundo mês: os meses anteriores permanecem aplicados, o processo para, e a tela pede prévia
    nova (nenhuma chamada depois do conflito).
45. Candidata sem espelho aparece como `no_mirror` e **nenhuma perna é criada** (contagem de
    lançamentos da casa inalterada).

**Tela**

46. Item "IA" no menu, com ícone próprio do projeto (sem emoji, sem biblioteca de ícones).
47. A janela de trabalho é uma só, no topo, e vale para as três seções.
48. Categorias novas aparecem em bloco separado, **marcadas** por padrão, e desmarcá-las remove também
    as palavras-chave delas do que será aplicado.
49. Copiar coloca no clipboard exatamente o texto exibido; Baixar gera `.md` com o mesmo conteúdo.
50. O aviso de envio a terceiros está visível **antes** de qualquer botão de copiar/baixar.
51. Confirmar fica desabilitado quando a prévia não tem nada para aplicar.
52. Contraste AA e foco visível nas três seções (checklist do `designer-ui`).
53. E2E Playwright: exportar 3 meses → colar JSON com categoria nova e palavras de conta → prévia →
    desmarcar uma categoria → confirmar → reprocessar → a categoria desmarcada **não** existe em
    `/categorias`, a criada existe com suas palavras, e o par virou transferência em `/transferencias`.

---

## 10. Emenda de 21/09/2026 — os oito achados do `arquiteto` contra o código real

A spec foi escrita a partir do domínio, e o `arquiteto` a confrontou com o código. Oito pontos não se
sustentavam. **Esta emenda é normativa e prevalece sobre o corpo acima** onde houver divergência; o
corpo foi corrigido nos pontos perigosos e preservado no resto, porque spec é histórico de decisão.

| # | O que a spec dizia | O que o código impõe | Resolução |
|---|---|---|---|
| **A1** | "3 meses × (prévia + execução) = 6 chamadas por balde, folgado" (§8.6) | `Burst: 3` em `AutoCategorize` e `TransferDetect` (`ratelimits.go:197,205`), recompondo 1 token/min: o caminho feliz toma **429 determinístico** | **`Burst: 3 → 6`** nas duas regras. Menor número que faz o uso legítimo caber; segue 4× abaixo do pool de 25 e 10× abaixo da cota de 60/h. **Decisão do usuário, 21/09/2026.** Exige ratificação do `revisor-seguranca` (altera número fixado por revisão anterior) |
| **A2** | `from`/`to` como `CivilDate` | nenhuma das 43 rotas tem `from`/`to`; toda janela é `YearMonth` de **competência** (ADR-023c) | **`fromMonth`/`toMonth`, `YearMonth`**, máximo 3 meses inclusive. **Decisão do usuário, 21/09/2026.** Elimina a ambiguidade de "3 meses a partir do dia 15" e torna a derivação dos meses da §5.2 uma identidade |
| **A3** | `PERIOD_TOO_LONG`, `EMPTY_PAYLOAD` como códigos de erro | `ErrorCode` é enum **fechado**; código próprio só quando a **ação da tela** difere (D4 da spec 0003) | **`400 VALIDATION_FAILED`** com `fields.toMonth` e `fields.payload` |
| **A4** | corpo grande = 400 (critério 20) | `MaxBytesReader` → `ErrPayloadTooLarge` → **413** (`decode.go:460`) | **413**, como nas outras 43 rotas |
| **A5** | "item com 19 palavras recebendo 3: entram as 2 primeiras" (critério 15) | `MaxKeywordsPerOwner = 20`; 19 + 2 = **21** | **18 + 3 → entram 2**; e caso novo **19 + 3 → entra 1**. Erro aritmético meu |
| **A6** | a §4.2 não previa `categoryId` de **grupo com subcategoria ativa** | spec 0005 §12 proíbe palavra em grupo com filha ativa (`category/service.go:400-414`); os ids de grupo **vão no prompt** | linha nova na §4.2: motivo **`group_has_children`**. Sem ela, o import seria a única porta do produto a gravar onde as outras três recusam |
| **A7** | `PARENT_NOT_GROUP` na §4.3 | a categoria nova é chaveada por **nome**, não por id; um `group` com nome de subcategoria simplesmente não acha grupo e cai em "grupo não existe" | **removido do enum.** Enum fechado com valor inalcançável é armadilha para a tela e para o teste |
| **A8** | "origem marcando que veio do import de IA" dentro de `category.updated` (§8.7) | `audit.Entry` não tem campo de metadados (`audit/types.go:179-188`), e a §7 proíbe coluna nova | três níveis: `category.created` por criação, `category.updated`/`account.updated` por item, e **uma** entrada **`ai.keyword_import_confirmed`** por execução (entidade `household`) — é ela a origem. Zero DDL |

**Mais duas correções de estilo, a favor do que o `openapi.yaml` já faz:** os `reason`/`outcome` são
`lower_snake` (como `below_threshold`, `no_mirror`), não `SCREAMING_SNAKE` — o que também elimina a
colisão com o `ErrorCode` `KEYWORD_TAKEN`; e a recusa carrega **`ownerId`** (como `KeywordConflict`),
nunca um `detail` de texto livre vindo do servidor.

### 10.1 Acréscimo pedido pelo `designer-ui`: o impacto precisa de denominador

A §4.4 media `transferCandidates` em absoluto. A tela precisa dizer **"87 de 212"**, e o limiar de
atenção é proporcional (`>= 10` **e** `>= 10%`) — em absoluto, `nubank` com 24 acertos legítimos
gritaria igual a `pagamento` com 87. `KeywordImportTotals` ganha **`periodTransactions`**: os
lançamentos vivos `income`/`expense` da janela, o universo da medição. Sem ele o limiar cai para
absoluto e a frase perde o "de 212" — pior, e evitável por um inteiro.

### 10.2 Fatiamento da entrega (recomendação do `arquiteto`, adotada)

| Fatia | Entrega | Critérios §9 |
|---|---|---|
| **E9a — Exportar** | `GET /ai/export-prompt` + tela `/ia` com a janela e a seção Exportar | 1–10, 46, 47, 49, 50, 52 (parcial) |
| **E9b — Importar** | `preview` + `confirm` + a seção Importar | 11–36, 48, 51, 52 (parcial) |
| **E9c — Reprocessar** | orquestração no frontend, `Burst` de A1, E2E ponta a ponta | 37–45, 53 |

Três fatias porque são **três superfícies de risco diferentes**, e uma revisão de segurança única
seria uma revisão rasa das três: a E9a não escreve **nada** (nem `UnitOfWork`, nem `Auditor`); a E9b
é onde mora o risco inteiro (JSON hostil que cria estrutura); a E9c não tem backend novo, mas altera
um número de segurança. A E9a **entrega valor sozinha**: com o prompt no ar, a pessoa já aplica o
resultado à mão nos diálogos que já existem — a parte difícil (decidir) sai do caminho antes de
qualquer escrita nova existir.

### 10.3 Pacotes Go

`internal/aiprompt` (leitura pura, molde de `internal/dashboard`: **sem** `Transactor` e **sem**
`Auditor` no construtor) e `internal/aiimport` (escrita transacional, molde de `internal/importer`).
Dois pacotes, e não um `internal/ai` com dois arquivos, para que "o export não escreve" seja
**verificável pelo compilador** em vez de convenção de arquivo. O nome `ai` sozinho foi recusado: um
pacote com esse nome que nunca fala com IA é a primeira coisa que alguém tenta "completar" depois.

### 10.4 Criação de categoria: o caminho é `category.Service.Create`, sem refatoração

`UnitOfWork.Do` é **reentrante** (`gormstore/uow.go:36-39`): chamado de dentro de uma transação, ele
entra nela em vez de abrir outra. Então o import chama `Create` diretamente e herda de graça o teto
de 200, a resolução do pai na casa do token, a recusa do terceiro nível, a herança da natureza e a
auditoria. A **única** extração necessária é `SetKeywords` em `category.Service` e `account.Service`,
reusando `podeReceberPalavras`/`gravarPalavras`/`registrar` sem alterar uma linha deles — e o import
cria a folha com `Keywords: nil`, gravando **todas** as palavras (de categoria nova e existente) por
um caminho só, porque dois caminhos de palavra divergiriam.

**A pendência da §7.1 (`NameTaken` e arquivadas) se resolve sem tocar no repositório:** o import lê
`List(householdID, includeArchived=true)` — no máximo 200 linhas — **dentro da transação** e monta um
índice `(pai, nomeNorm) -> {id, kind, arquivada, temFilhaAtiva}`. É o padrão que a semente já provou
em 18/09/2026 (`category/seed.go:442-466`, achado A4 daquela revisão). Mexer no `NameTaken` quebraria
`Unarchive`, que depende da semântica "livre **entre as ativas**". O índice ainda responde um caso
que a §4.3 não escrevia: **grupo existente porém arquivado** → `name_taken_archived`, orientando
desarquivar (folha ativa pendurada em grupo invisível é o estado que `ErrParentArchived` existe para
impedir).

### 10.5 Reprocessamento: confirmado sem rota nova

Os quatro eixos foram validados. Rate limit **não** se sustentava (A1, corrigido); 409, derivação dos
meses e a ordem obrigatória se sustentam. Um endpoint orquestrador foi **recusado**: o único
argumento real a favor era o rate limit, que se resolve com uma linha de configuração, contra uma
rota nova que traria corpo novo, relatório consolidado novo, história de transação ambígua ("é uma
transação ou seis?") e uma requisição segurando conexão do pool por seis transações em série. O
consolidado da §5.2 é somado no cliente porque **não é dinheiro** — são contagens de linhas
afetadas, cada uma completa e autoritativa na sua resposta; a regra de somar num lugar só fala de
centavos, e nenhum centavo aparece no reprocessamento.

### 10.6 Dois desvios do `designer-ui`, ratificados aqui

1. **O rótulo E7 estava tomado duas vezes** — `docs/DESIGN.md:1703` (investimentos, spec 0006) e
   `PLANOS.md` (convites e membros, decisão D12 de 12/09/2026). A entrega passa a ser **E9**, o
   primeiro rótulo livre. É a lição de 18/09/2026 sobre numeração pegando de novo, agora em rótulo de
   entrega e não em número de ADR.
2. **A barra inferior do celular fecha em 7 células.** Medido: com 8 células, a célula cai a 45,4 px
   a 393 px de viewport, abaixo dos 47 px que o rótulo exige — os rótulos de **toda** a navegação
   apagariam em praticamente todo celular em pé. Regra nova: **ferramenta não ocupa célula da barra**.
   No desktop `IA` é o primeiro item do grupo "ferramentas" na lateral; abaixo de 52rem ele vive no
   menu do usuário, cujo gatilho passa a se chamar `Menu de {nome}`.

### 10.7 Emendas de contrato da implementação da E9b (21/09/2026)

Registradas pelo `dev-backend-go` ao implementar `internal/aiimport`, spec-first. Onde o contrato e o
corpo desta spec divergem, **vale o contrato** (`backend/api/openapi.yaml`), e esta subseção é o
registro do porquê.

1. **`POST /ai/keyword-import/confirm` responde 409 `CONFLICT`** quando o estado mudou entre a leitura
   e a escrita da mesma transação (`ErrNameTaken`, `ErrKeywordTaken`, `ErrKeywordsOnGroupWithChildren`
   ou `ErrNotFound` vindos de `SetKeywords`/`Create` **depois** da pré-checagem pelo índice). Nada é
   gravado; a tela pede prévia nova.
2. **422 em `fields.toMonth` nas duas rotas**, para o estouro da agregação (`ErrTooManyDescriptionGroups`)
   e para o orçamento do matcher — nunca medição parcial. **Consequência que contraria a §4.1:** como
   o contrato exige `totals.periodTransactions` nas **duas** respostas, o confirm **usa** a janela (uma
   `GroupByDescription` fora da transação) — a §4.1 dizia "validados e não usados". O contrato
   prevaleceu; a frase da §4.1 está superada.
3. **`KeywordImportRejectedKeyword.keyword` é string livre**, não o schema `Keyword`: em
   `invalid_keyword` volta a forma bruta — neutralizada pela allowlist `aiprompt.Drawable` e truncada
   em 40 runas — que por definição não satisfaz `Keyword`. Não entra em log. **Correção da revisão
   de segurança (21/09/2026):** não é a única superfície de eco — `recusarEntrada` devolve a forma
   bruta neutralizada em **todas** as recusas de entrada inteira (`item_not_found`, `item_archived`,
   `name_mismatch`, `group_has_children`), e `items[].id` ecoa o id quando canônico. Tudo passa por
   `Drawable` ou é de forma fixa; a redação anterior estava incompleta, o código não.
4. **`KeywordImportNewCategory.categoryId`** (nullable, obrigatório): preenchido em
   `merged_into_existing` nas duas rotas e em `created` só no confirm (na prévia a categoria ainda não
   existe).
5. **`KeywordImportItem.id` vem `""`** quando o id do JSON não é uuid canônico: texto que não é uuid
   não volta para o app. A tela casa a linha pelo índice, não pelo id.
6. **`impact.byKeyword`** — a medição da §4.4 é **por palavra**, uma entrada para cada palavra em
   `added` do item, na mesma ordem; `impact.transferCandidates` do item é a **união** (a soma de
   `byKeyword` pode passar dela). O rascunho original do contrato tinha posto o `impact` no item, e a
   tela ficava sem saber qual palavra era a genérica — que é a única razão de a medição existir.
7. **`ai.keyword_import_confirmed` é gravada em toda execução que comita, mesmo com zero escritas** —
   mesmo desenho de `investments/detect`: a intenção é parte do rastro. O "banco inalterado" do
   critério 12 vale para taxonomia e palavras, não para a auditoria.
8. **`duplicate_in_payload`:** o **primeiro** bloco com um `grupo > folha` toma o caminho **mesmo
   quando é recusado**; um segundo bloco válido não "conserta" o primeiro. Literal à §4.3 (8).
9. **Interfaces estreitas, e uma garantia do compilador:** `AccountWriter` **não tem `Create`** — conta
   não nasce pelo import é verificável em compilação, não em revisão. `CategoryStore` tem só `List` e
   `ListKeywords` (o índice em memória responde o dono; `gravarPalavras` dentro de `SetKeywords` chama
   `KeywordOwners` por si).
10. **Conta de heap MEDIDA, não presumida.** O ADR-036(f) anunciava 28 operações ≈ 1,23–1,29 GiB
    presumindo 47 MiB por prévia. Medido (`impact_memoria_test.go`, teto do produto, matcher vivo):
    **3,9 MiB** por prévia, ≤ ~12 MiB com as 5.000 linhas agregadas e o índice. Quadro real:
    `25 × 45–47 MiB + 3 × ~12 MiB ≈ 1,13–1,18 GiB` por casa (+3% sobre a E9a). **Nenhum `Burst`
    precisa apertar** — decisão registrada no ADR-036(f) e **pendente de ratificação do
    `revisor-seguranca`**.
11. **`>` em nome de categoria existente:** a assimetria com `POST /categories` **não** foi resolvida
    (fora da fatia). Categoria existente com `>` no nome é alcançável por id em `categoryKeywords` (o
    caminho de conferência é comparado inteiro) e **nunca** por `newCategories` (`invalid_name`
    claro, nunca `ref` ambíguo). Coberto por teste.

### 10.8 Refinamento da §5.2 na implementação da E9c (21/09/2026): duas fases, não "mês a mês"

A §5.2 (3) dizia "executa mês a mês, cada mês na ordem da §5.1" — ou seja, jul: detect → categorize;
ago: detect → categorize; set: idem. A implementação achou uma borda que essa ordem não cobre: a
detecção procura o **espelho a ±3 dias**, então ela cruza a fronteira do mês. Categorizar julho e só
depois detectar agosto deixa um par com uma perna em 31/07 ter a categoria de julho zerada **depois**
de reportada como categorizada — o banco fica certo, mas o relatório de julho mente.

**Ordem adotada: duas fases, cada uma mês a mês.** Fase 1 = `POST /transfers/detect` em todos os
meses da janela; fase 2 = `POST /transactions/auto-categorize` em todos os meses. É a leitura mais fiel
ao princípio da §5.1 — **nenhuma** categorização acontece antes de **toda** conversão — e elimina a
borda. A prévia roda as seis chamadas `dryRun` em paralelo (não escrevem); a execução é estritamente
sequencial, `await` a `await`, e para no primeiro erro.

Consequências no 409 (§5.2 (4)): o que ficou aplicado é descrito **por etapa e por mês** ("as
transferências de julho, agosto e setembro foram aplicadas; a categorização de julho também; a de
agosto não foi; a de setembro não chegou a rodar"). Três estados por célula: `feito` (respondeu 200),
`não aplicado` (respondeu erro — transação desfeita) e `sem resposta` (rede caiu — **não se sabe**, e
a tela não afirma o que não sabe). Execução em curso ou parada **não** é descartada pela regra de
frescor: o registro de uma aplicação pela metade precisa ficar na tela.

Um efeito colateral honesto, anotado na própria tela: a prévia da categorização é medida **antes** das
transferências serem aplicadas, então quando pares e categorizados são ambos maiores que zero, o
número real de categorizados pode ser menor que o da prévia (as pernas convertidas saem do universo).
É o critério 39 acontecendo por construção — e é por isso que o relatório final usa os números da
**execução**, nunca os da prévia.
