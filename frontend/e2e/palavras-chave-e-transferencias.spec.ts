import { expect, type Locator, type Page, test } from '@playwright/test'

/** Ponta a ponta da E2c (spec 0005 §8.12), contra a API Go real:
 *
 *  1. cadastrar a palavra-chave «zumbra» em "QA Feira" → importar um extrato
 *     com "ZUMBRALINO DO SEU JOSE" → a linha vem com a categoria JÁ
 *     selecionada e a proveniência em texto (`88% · «zumbra»`) → confirmar →
 *     em `/lancamentos` o lançamento tem a categoria;
 *  2. duas contas A e B, B com a palavra-chave «nubank» → o extrato de A com
 *     "Transferência enviada pelo Pix - NUBANK" cai no bloco "Transferências
 *     detectadas", barrado → "Aceitar as N transferências sugeridas" → o par
 *     nasce → `/transferencias` mostra o par, o líquido e a frase de direção →
 *     o extrato de B com a linha espelhada vem `transferencia_ja_registrada`
 *     com "Vincular…" como default → confirmar → `vinculadas` no resultado →
 *     reimportar B cai em `duplicado_exato`;
 *  3. `/lancamentos` com lançamentos sem categoria → "Categorizar
 *     automaticamente" → prévia "N de M recebem categoria" → confirmar →
 *     toast com o número REAL → a faixa de pendência diminui;
 *  4. a mesma palavra-chave em outra categoria → 409 na tela, com a dona pelo
 *     NOME;
 *  5. duas contas importadas ANTES de terem palavra-chave (o Pix entre elas
 *     entra como despesa numa e receita na outra) → cadastrar em cada conta a
 *     palavra da PRÓPRIA conta → `/transferencias` → "Reprocessar
 *     transferências" → prévia com 1 par → confirmar → toast → o par aparece na
 *     tabela e o `summary` de `/lancamentos` deixa de contar os 300,00 (spec
 *     0005 §13.5, critério 10).
 *
 *  O que só este nível prova: a pontuação `88` sai do `internal/textmatch`
 *  real (a tabela da §3 da spec), o pareamento da perna existente e o `link`
 *  acontecem no servidor sobre SQLite de verdade, o `<dialog>` e o `<select>`
 *  são os nativos do Chromium (no jsdom são polyfill), e o `KeywordsField`
 *  responde ao teclado de um navegador real.
 *
 *  Compartilha a casa do projeto `setup` (ver `e2e/sessao.setup.ts`), roda em
 *  série e usa nomes e um MÊS próprios (julho de 2026; junho para o cenário 5)
 *  para não colidir com o que as outras specs criaram em agosto e setembro.
 *
 *  ## Grupos próprios e nomes inventados (ADR-033, 18/09/2026)
 *
 *  Este spec cadastrava as palavras em "Alimentação", "Saúde", "Transporte" e
 *  "Serviços", e as fixtures se chamavam MERCADO, FARMACIA, SALARIO, POSTO,
 *  PADARIA e CAFE. A semente de categorias derrubou as duas coisas:
 *
 *  - os quatro grupos nasceram **com filhas**, e grupo com filha esconde o
 *    campo de palavras-chave (spec 0005 §12) e some do `<select>` (§13). O
 *    assunto destes cenários é a UX do campo de fichas e o fluxo do 409, não a
 *    taxonomia — então o spec cria **grupos próprios sem filhas** (`QA Feira`,
 *    `QA Trajeto`, `QA Cuidado`, `QA Servico`) e usa eles;
 *  - as seis descrições passaram a ser reconhecidas de fábrica, e o cenário 3
 *    precisa de linhas que cheguem SEM categoria. Os nomes novos —
 *    `ZUMBRALINO`, `VRANDIX`, `TARVIN`, `PLINTAQ`, `KREVOL`, `GLIMPO` — foram
 *    conferidos contra o motor real (`internal/textmatch` + a lista de
 *    `internal/category/seed.go`) e não alcançam o limiar de 80 em nenhum dos
 *    dois lados do dinheiro.
 *
 *  A aritmética do cenário 1 foi **preservada de propósito**: «zumbra» (6
 *  runas) dentro de `ZUMBRALINO` (10) dá os mesmos 88 que «supermercado» dava
 *  em `MERCADO`, pela regra 2 da §3 (contenção total, 70 + 30·6/10). A
 *  pontuação continua saindo do motor, e continua sendo 88. */

test.describe.configure({ mode: 'serial' })

const CONTA_A = 'Itaú QA'
const CONTA_B = 'Nubank QA'
const MES = '2026-07'

/** Cenário 5: outras duas contas e outro mês, para o reprocessamento não
 *  enxergar nada do que os cenários 1–4 gravaram em julho. */
const CONTA_C = 'Bradesco QA'
const CONTA_D = 'Inter QA'
const MES_REPROCESSAR = '2026-06'

/** Os grupos PRÓPRIOS deste spec, criados sem subcategoria: são eles que
 *  aceitam palavra-chave e aparecem como opção no `<select>`. Ver o cabeçalho
 *  para o porquê de não serem mais os grupos da semente. */
const GRUPO_FEIRA = 'QA Feira'
const GRUPO_TRAJETO = 'QA Trajeto'
const GRUPO_CUIDADO = 'QA Cuidado'
const GRUPO_SERVICO = 'QA Servico'

/** Extratos no formato do Nubank (`Data,Valor,Identificador,Descrição`;
 *  negativo é saída). O Identificador é a chave natural da deduplicação, por
 *  isso cada linha tem o seu e o prefixo é diferente do das outras specs. */
type Linha = { dia: string; valor: string; id: string; descricao: string }

function extratoCSV(linhas: readonly Linha[], mes = '07'): Buffer {
  const cabecalho = 'Data,Valor,Identificador,Descrição\n'
  const corpo = linhas
    .map(
      (l) =>
        `${l.dia}/${mes}/2026,${l.valor},22222222-2222-4222-8222-2222222222${l.id},${l.descricao}\n`,
    )
    .join('')
  return Buffer.from(cabecalho + corpo, 'utf-8')
}

/** Cenário 1: uma linha que casa por APROXIMAÇÃO com «zumbra» (88, regra 2 da
 *  §3 — `zumbra` inteira dentro de `zumbralino`) e três que não casam com nada,
 *  nem com a semente: ficam sem categoria para o cenário 3. */
const EXTRATO_CATEGORIAS: readonly Linha[] = [
  { dia: '03', valor: '-50.00', id: '01', descricao: 'ZUMBRALINO DO SEU JOSE' },
  { dia: '07', valor: '-19.90', id: '02', descricao: 'VRANDIX QA EXEMPLO' },
  { dia: '12', valor: '1200.00', id: '03', descricao: 'TARVIN QA EXEMPLO' },
  { dia: '20', valor: '-33.00', id: '04', descricao: 'PLINTAQ QA EXEMPLO' },
]

/** Cenário 2, extrato de A: duas saídas para a Nubank (o sanitizador do
 *  servidor troca o prefixo verboso por "Pix enviado") e uma compra comum, que
 *  fica no bloco "Prontas" para provar que "aceitar todas" não a toca. */
const EXTRATO_A: readonly Linha[] = [
  { dia: '15', valor: '-150.00', id: '11', descricao: 'Transferência enviada pelo Pix - NUBANK' },
  { dia: '16', valor: '-12.00', id: '12', descricao: 'KREVOL QA EXEMPLO' },
  { dia: '22', valor: '-25.00', id: '13', descricao: 'Transferência enviada pelo Pix - NUBANK' },
]

/** Cenário 2, extrato de B: as duas entradas espelhadas (a segunda um dia
 *  depois — a janela é de ±3 dias) e uma compra comum. */
const EXTRATO_B: readonly Linha[] = [
  { dia: '15', valor: '150.00', id: '21', descricao: 'Transferência recebida pelo Pix - ITAU QA' },
  { dia: '18', valor: '-8.50', id: '22', descricao: 'GLIMPO QA EXEMPLO' },
  { dia: '23', valor: '25.00', id: '23', descricao: 'Transferência recebida pelo Pix - ITAU QA' },
]

/** Cenário 5: o mesmo Pix visto de cada lado, em junho. Sem palavra-chave de
 *  conta na hora da importação, a saída de C entra como despesa e a entrada de
 *  D como receita — exatamente o estado que o reprocessamento conserta. As
 *  descrições não dizem o banco de destino (é "BRUNO" dos dois lados), e por
 *  isso a palavra cadastrada depois é a da PRÓPRIA conta. */
const EXTRATO_C: readonly Linha[] = [
  { dia: '10', valor: '-300.00', id: '51', descricao: 'Pix enviado - BRUNO' },
]
const EXTRATO_D: readonly Linha[] = [
  { dia: '10', valor: '300.00', id: '61', descricao: 'Pix recebido de BRUNO' },
]

// ------------------------------------------------------------- utilitários

/** O `<input>` do `KeywordsField`. `exact` porque a lista de fichas se chama
 *  "Palavras-chave adicionadas" e casaria por substring. */
function campoDePalavras(escopo: Page | Locator): Locator {
  return escopo.getByLabel('Palavras-chave', { exact: true })
}

function fichas(escopo: Page | Locator): Locator {
  return escopo.getByRole('list', { name: 'Palavras-chave adicionadas' }).getByRole('listitem')
}

/** Cria um GRUPO de despesa sem subcategoria — o dono de palavra-chave e o
 *  destino selecionável que os grupos da semente deixaram de ser. */
async function criarGrupoDeDespesa(page: Page, nome: string): Promise<void> {
  await page.goto('/categorias')
  await page.getByRole('button', { name: 'Novo grupo', exact: true }).click()
  const dialogo = page.locator('dialog[open]')
  await dialogo.getByLabel('Nome').fill(nome)
  // Explícito, e não pelo padrão do formulário: um grupo que nascesse de
  // receita apareceria no `<select>` errado e o teste morreria longe daqui.
  await dialogo.getByLabel('Natureza').selectOption('expense')
  await dialogo.getByRole('button', { name: /^Criar/ }).click()
  await expect(dialogo).toHaveCount(0)
  await expect(
    page.getByRole('region', { name: 'Despesas' }).getByText(nome, { exact: true }),
  ).toBeVisible()
}

async function criarConta(page: Page, nome: string, palavra?: string): Promise<void> {
  await page.goto('/contas')
  await page.getByRole('button', { name: 'Nova conta', exact: true }).click()
  const dialogo = page.getByRole('dialog')
  await expect(dialogo.getByRole('heading', { name: 'Nova conta' })).toBeVisible()

  await dialogo.getByLabel('Nome').fill(nome)
  await dialogo.getByLabel('Tipo').selectOption('checking')
  if (palavra !== undefined) {
    await campoDePalavras(dialogo).fill(palavra)
    await campoDePalavras(dialogo).press('Enter')
    await expect(fichas(dialogo)).toHaveText([palavra])
  }

  await dialogo.getByRole('button', { name: 'Criar conta' }).click()
  await expect(page.getByRole('row', { name: new RegExp(nome) })).toBeVisible()
}

function criarContaComPalavra(page: Page, nome: string, palavra: string): Promise<void> {
  return criarConta(page, nome, palavra)
}

/** Acrescenta uma palavra-chave a uma conta que JÁ existe — o cenário 5
 *  cadastra a palavra depois da importação, que é o bug real. */
async function adicionarPalavraNaConta(page: Page, nome: string, palavra: string): Promise<void> {
  await page.goto('/contas')
  await page.getByRole('button', { name: `Editar ${nome}` }).click()
  const dialogo = page.locator('dialog[open]')
  await expect(dialogo.getByRole('heading', { name: 'Editar conta' })).toBeVisible()
  await campoDePalavras(dialogo).fill(palavra)
  await campoDePalavras(dialogo).press('Enter')
  await expect(fichas(dialogo).filter({ hasText: palavra })).toHaveCount(1)
  await dialogo.getByRole('button', { name: 'Salvar' }).click()
  await expect(page.getByText('Conta atualizada.')).toBeVisible()
  await expect(dialogo).toHaveCount(0)
}

/** Envia um extrato pelo passo 1 e deixa a página na revisão. */
async function enviarExtrato(
  page: Page,
  conta: string,
  nome: string,
  linhas: readonly Linha[],
  mes = '07',
) {
  await page.goto('/importar')
  await expect(
    page.getByRole('heading', { level: 1, name: 'Importar extrato ou fatura' }),
  ).toBeVisible()

  await page.getByLabel('Conta de destino').selectOption({ label: conta })
  await page.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
    name: nome,
    mimeType: 'text/csv',
    buffer: extratoCSV(linhas, mes),
  })
  await page.getByRole('button', { name: 'Analisar arquivo' }).click()
  await expect(
    page.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
  ).toBeVisible()
  await expect(page).toHaveURL(/\/importar\/[0-9a-f-]+\/revisar$/)
}

/** O `<select>` de decisão de uma linha da revisão, pelo nome acessível que a
 *  tela dá a ele ("Decisão para <descrição>, <dd/mm>, <valor falado>"). */
function decisaoDe(page: Page, descricao: string, data: string): Locator {
  return page.getByRole('combobox', { name: new RegExp(`^Decisão para ${descricao}, ${data},`) })
}

function categoriaDe(page: Page, descricao: string, data: string): Locator {
  return page.getByRole('combobox', { name: new RegExp(`^Categoria de ${descricao}, ${data},`) })
}

/** A faixa de "N lançamentos sem categoria" de `/lancamentos`. */
function faixaDePendencia(page: Page): Locator {
  return page.getByRole('alert').filter({ hasText: /sem categoria/ })
}

/** A descrição acessível como o PRÓPRIO Chromium a calcula (CDP), e não como
 *  o Playwright a reimplementa em `toHaveAccessibleDescription`.
 *
 *  Achado do QA em 17/09/2026: o contador do `KeywordsField` é um `<output>`
 *  apontado por `aria-describedby`. A árvore nativa do Chromium inclui o texto
 *  dele na descrição do input ("0 de 20 Prefira o nome…"), mas a implementação
 *  de accname do Playwright trata `<output>` como controle de formulário e
 *  devolve vazio para ele — a asserção por `toHaveAccessibleDescription`
 *  reprovava um comportamento que o navegador (e o leitor de tela) entregam
 *  certo. Perguntar ao navegador é o que este nível de teste existe para fazer. */
async function descricaoNativa(page: Page, elemento: Locator): Promise<string> {
  const id = await elemento.getAttribute('id')
  if (!id) throw new Error('o elemento precisa de id para ser achado pelo CDP')
  const cdp = await page.context().newCDPSession(page)
  try {
    await cdp.send('Accessibility.enable')
    const { result } = await cdp.send('Runtime.evaluate', {
      expression: `document.getElementById(${JSON.stringify(id)})`,
    })
    if (!result.objectId) throw new Error(`nenhum elemento com id ${id}`)
    const { nodes } = await cdp.send('Accessibility.getPartialAXTree', {
      objectId: result.objectId,
      fetchRelatives: false,
    })
    return String(nodes[0]?.description?.value ?? '')
  } finally {
    await cdp.detach()
  }
}

// ------------------------------------------------------------------ cenários

test.describe('palavras-chave e categorização na importação', () => {
  test('os quatro grupos próprios do spec existem, sem subcategoria', async ({ page }) => {
    // Grupo da SEMENTE não serve aqui: os 14 que têm filhas escondem o campo de
    // palavras-chave (spec 0005 §12) e não são opção de `<select>` (§13). Quem
    // testa o campo de fichas e o 409 precisa de um grupo FOLHA, e é isso que
    // estes quatro são.
    for (const nome of [GRUPO_FEIRA, GRUPO_TRAJETO, GRUPO_CUIDADO, GRUPO_SERVICO]) {
      await criarGrupoDeDespesa(page, nome)
    }
  })

  test('a palavra-chave «zumbra» entra em QA Feira pelo diálogo, com teclado', async ({ page }) => {
    await page.goto('/categorias')
    await page.getByRole('button', { name: `Editar ${GRUPO_FEIRA}` }).click()

    // `<dialog>` NATIVO, aberto — no jsdom ele é polyfill; aqui é o do Chromium.
    const dialogo = page.locator('dialog[open]')
    await expect(dialogo.getByRole('heading', { name: 'Editar categoria' })).toBeVisible()

    const input = campoDePalavras(dialogo)
    // O campo se descreve para quem não vê: contador, dica e instrução de
    // teclado — conferido na árvore de acessibilidade NATIVA do Chromium.
    const ids = (await input.getAttribute('aria-describedby'))?.split(' ') ?? []
    expect(ids).toHaveLength(3)
    expect(await descricaoNativa(page, input)).toMatch(
      /^0 de 20 Prefira o nome do estabelecimento.*Use as setas para percorrer as palavras e Backspace para remover\.$/,
    )

    // Enter adiciona e NÃO submete: o diálogo continua aberto.
    await input.fill('zumbra')
    await input.press('Enter')
    await expect(fichas(dialogo)).toHaveText(['zumbra'])
    await expect(input).toHaveValue('')
    await expect(dialogo).toBeVisible()
    await expect(dialogo.getByRole('status')).toHaveText('1 de 20')

    // Vírgula também adiciona; ← no início do input leva ao × da última ficha;
    // Delete remove e devolve o foco à ficha anterior; → volta ao input.
    await input.pressSequentially('zumbrete,')
    await expect(fichas(dialogo)).toHaveText(['zumbra', 'zumbrete'])
    await input.press('ArrowLeft')
    await expect(dialogo.getByRole('button', { name: 'Remover zumbrete' })).toBeFocused()
    await page.keyboard.press('Delete')
    await expect(fichas(dialogo)).toHaveText(['zumbra'])
    await expect(dialogo.getByRole('button', { name: 'Remover zumbra' })).toBeFocused()
    await page.keyboard.press('ArrowRight')
    await expect(input).toBeFocused()

    // Os × não são parada de Tab: um Tab a partir do input sai do campo.
    await expect(dialogo.getByRole('button', { name: 'Remover zumbra' })).toHaveAttribute(
      'tabindex',
      '-1',
    )

    await dialogo.getByRole('button', { name: 'Salvar' }).click()
    await expect(page.getByText('Categoria atualizada.')).toBeVisible()
    await expect(dialogo).toHaveCount(0)

    // Reabrir traz a palavra gravada pelo servidor.
    await page.getByRole('button', { name: `Editar ${GRUPO_FEIRA}` }).click()
    await expect(fichas(page.locator('dialog[open]'))).toHaveText(['zumbra'])
    await page.keyboard.press('Escape')
    await expect(page.locator('dialog[open]')).toHaveCount(0)
  })

  test('as duas contas existem, cada uma com a palavra-chave que a outra reconhece', async ({
    page,
  }) => {
    await criarContaComPalavra(page, CONTA_A, 'itau')
    await criarContaComPalavra(page, CONTA_B, 'nubank')
  })

  test('a linha vem com a categoria sugerida e a proveniência em texto; confirmar grava a categoria', async ({
    page,
  }) => {
    await enviarExtrato(page, CONTA_A, 'NU_categorias.csv', EXTRATO_CATEGORIAS)

    await expect(page.getByRole('status').filter({ hasText: '4 linhas lidas' })).toBeVisible()
    await expect(
      page.getByText(
        '0 precisam da sua decisão · 0 transferências detectadas · 4 prontas para importar · 0 ficam de fora.',
      ),
    ).toBeVisible()

    // A sugestão vive no CONTROLE: o <select> nativo já vem com QA Feira
    // selecionada — e a pontuação é texto, com a palavra que decidiu.
    const aproximado = categoriaDe(page, 'ZUMBRALINO DO SEU JOSE', '03/07')
    await expect(aproximado).toHaveRole('combobox')
    await expect(aproximado.locator('option:checked')).toHaveText(GRUPO_FEIRA)
    await expect(page.getByText('88% · «zumbra»')).toBeVisible()
    // Linha sem correspondência não ganha nada — nem da palavra deste spec, nem
    // das palavras da semente.
    await expect(
      categoriaDe(page, 'VRANDIX QA EXEMPLO', '07/07').locator('option:checked'),
    ).toHaveText('Sem categoria')

    // Limpar a sugestão apaga a proveniência; voltar a QA Feira a traz de volta.
    await aproximado.selectOption({ label: 'Sem categoria' })
    await expect(page.getByText('88% · «zumbra»')).toHaveCount(0)
    await aproximado.selectOption({ label: GRUPO_FEIRA })
    await expect(page.getByText('88% · «zumbra»')).toBeVisible()

    // Sem `decisions` na requisição: aceitar a sugestão não é exceção. O que o
    // servidor grava é o `suggestedCategoryId` dele mesmo.
    const [confirmacao] = await Promise.all([
      page.waitForRequest(
        (r) => /\/imports\/[0-9a-f-]+\/confirm$/.test(r.url()) && r.method() === 'POST',
      ),
      page.getByRole('button', { name: 'Importar 4 lançamentos' }).click(),
    ])
    expect(confirmacao.postDataJSON()).toEqual({ decisions: [] })

    await expect(
      page.getByRole('heading', { level: 1, name: 'Importação concluída' }),
    ).toBeVisible()
    await expect(
      page.getByRole('status').filter({ hasText: '4 lançamentos importados' }),
    ).toBeVisible()

    // Em /lancamentos o lançamento TEM a categoria, e os outros três não.
    await page.getByRole('button', { name: 'Ver os lançamentos de julho' }).click()
    await expect(page).toHaveURL(new RegExp(`/lancamentos\\?mes=${MES}`))
    // Desde a emenda §19 o nome da categoria é o texto de um BOTÃO — o outro
    // estado fechado do mesmo controle —, e ele existe duas vezes por linha,
    // uma visível por faixa. A afirmação continua sendo "esta linha está em
    // QA Feira".
    await expect(
      page
        .getByRole('row', { name: /ZUMBRALINO DO SEU JOSE/ })
        .getByRole('button', { name: /^QA Feira\. Trocar categoria de ZUMBRALINO DO SEU JOSE,/ })
        .filter({ visible: true }),
    ).toHaveCount(1)
    // Desde a emenda §11 a célula vazia é o BOTÃO do atalho, e ele existe duas
    // vezes por linha (coluna e linha secundária do celular) — uma visível por
    // faixa de largura. O que se afirma aqui continua sendo "esta linha está
    // sem categoria"; o locator é que passou a apontar o controle.
    await expect(
      page
        .getByRole('row', { name: /VRANDIX QA EXEMPLO/ })
        .getByRole('button', { name: /^Sem categoria\. Categorizar VRANDIX QA EXEMPLO,/ })
        .filter({ visible: true }),
    ).toHaveCount(1)
    await expect(faixaDePendencia(page)).toContainText('3 lançamentos de julho estão sem categoria')
  })
})

test.describe('transferências detectadas, vínculo e /transferencias', () => {
  let idDaContaA = ''
  let idDaContaB = ''

  test('o extrato de A detecta a Nubank pela palavra-chave; "aceitar todas" cria os pares', async ({
    page,
  }) => {
    await enviarExtrato(page, CONTA_A, 'NU_A.csv', EXTRATO_A)

    await expect(
      page.getByText(
        '0 precisam da sua decisão · 2 transferências detectadas · 1 prontas para importar · 0 ficam de fora.',
      ),
    ).toBeVisible()
    await expect(
      page.getByRole('heading', { level: 2, name: 'Transferências detectadas' }),
    ).toBeVisible()
    await expect(page.getByRole('rowheader', { name: /Parece transferência · 2/ })).toBeVisible()

    // A evidência fala da conta pelo NOME, e a palavra entre aspas angulares.
    // Correspondência exata (100) não mostra pontuação.
    await expect(page.getByText('Parece transferência para Nubank QA · «nubank»')).toHaveCount(2)

    // Barrada: o default é "Não importar", num <select> NATIVO com palavras.
    const primeira = decisaoDe(page, 'Pix enviado - NUBANK', '15/07')
    const segunda = decisaoDe(page, 'Pix enviado - NUBANK', '22/07')
    await expect(primeira).toHaveValue('skip')
    await expect(primeira.locator('option:checked')).toHaveText('Não importar')
    await expect(primeira.locator('option')).toHaveText([
      'Não importar',
      'Registrar como transferência para Nubank QA',
      'Registrar como transferência para outra conta…',
      'Importar como despesa comum (não é transferência)',
    ])
    // Só a compra comum está marcada para entrar: o botão diz isso.
    await expect(page.getByRole('button', { name: 'Importar 1 lançamento' })).toBeVisible()

    const comum = page.getByRole('checkbox', { name: /KREVOL QA EXEMPLO/ })
    await expect(comum).toBeChecked()

    await page.getByRole('button', { name: 'Aceitar as 2 transferências sugeridas' }).click()

    await expect(primeira).toHaveValue('transfer')
    await expect(primeira.locator('option:checked')).toHaveText(
      'Registrar como transferência para Nubank QA',
    )
    await expect(segunda).toHaveValue('transfer')
    // …e em NENHUMA outra linha: a compra comum continua como estava.
    await expect(comum).toBeChecked()
    await expect(page.getByRole('button', { name: 'Desfazer o aceite de todas' })).toBeVisible()
    await expect(page.getByText('Inclui 2 transferências.')).toBeVisible()

    // O corpo leva `transfer` SEM `counterpartAccountId`: o servidor usa a sugerida.
    const [confirmacao] = await Promise.all([
      page.waitForRequest(
        (r) => /\/imports\/[0-9a-f-]+\/confirm$/.test(r.url()) && r.method() === 'POST',
      ),
      page.getByRole('button', { name: 'Importar 3 lançamentos' }).click(),
    ])
    const decisoes = (confirmacao.postDataJSON() as { decisions: { action: string }[] }).decisions
    expect(decisoes).toHaveLength(2)
    expect(decisoes.every((d) => d.action === 'transfer' && !('counterpartAccountId' in d))).toBe(
      true,
    )

    await expect(
      page.getByRole('heading', { level: 1, name: 'Importação concluída' }),
    ).toBeVisible()
    await expect(
      page.getByRole('status').filter({ hasText: '2 transferências registradas' }),
    ).toBeVisible()
  })

  test('/transferencias mostra o par com A → B, o líquido e a direção em palavras', async ({
    page,
  }) => {
    await page.goto(`/transferencias?mes=${MES}`)

    await expect(page.getByRole('heading', { level: 1, name: 'Transferências' })).toBeVisible()
    await expect(page).toHaveTitle('Transferências · HomeFinance')
    await expect(page.getByRole('link', { name: 'Transferências', exact: true })).toHaveAttribute(
      'aria-current',
      'page',
    )

    // Sem filtro: a tabela de pares, com a seta apontando para quem mandou mais.
    const parLink = page.getByRole('link', {
      name: `Ver as transferências entre ${CONTA_A} e ${CONTA_B}`,
    })
    await expect(parLink).toBeVisible()
    const linhaDoPar = page.getByRole('row', { name: new RegExp(`${CONTA_A}.*${CONTA_B}`) }).first()
    // (`visible`: abaixo de 40rem os mesmos números reaparecem numa linha
    // secundária, escondida nesta largura.)
    await expect(
      linhaDoPar.getByText('175,00', { exact: true }).filter({ visible: true }),
    ).toHaveCount(2) // Enviado e Líquido
    await expect(
      linhaDoPar.getByText('0,00', { exact: true }).filter({ visible: true }),
    ).toHaveCount(1) // Recebido
    await expect(linhaDoPar.getByRole('cell', { name: '2', exact: true })).toBeVisible() // Transferências

    // A lista plana: cada PAR uma vez, com a direção dita em palavras para
    // quem não vê a seta.
    await expect(page.getByRole('row', { name: /Pix enviado - NUBANK/ })).toHaveCount(2)
    await expect(page.getByText(`de ${CONTA_A} para ${CONTA_B}`).first()).toBeAttached()
    await expect(
      page.getByText('Importação', { exact: true }).filter({ visible: true }),
    ).toHaveCount(2)

    // Abrir o par escreve as duas contas na URL...
    await parLink.click()
    await expect(page).toHaveURL(
      /conta=[0-9a-f-]+&contraparte=[0-9a-f-]+|contraparte=[0-9a-f-]+&conta=[0-9a-f-]+/,
    )
    const url = new URL(page.url())
    idDaContaA = url.searchParams.get('conta') ?? ''
    idDaContaB = url.searchParams.get('contraparte') ?? ''
    expect(idDaContaA).toMatch(/^[0-9a-f-]{36}$/)
    expect(idDaContaB).toMatch(/^[0-9a-f-]{36}$/)

    await expect(page.getByLabel('Conta', { exact: true }).locator('option:checked')).toHaveText(
      CONTA_A,
    )
    await expect(
      page.getByLabel('Outra conta', { exact: true }).locator('option:checked'),
    ).toHaveText(CONTA_B)

    // ...e o painel do par é uma <dl> orientada pela conta do filtro, com o
    // líquido em número COM sinal e a direção em frase.
    const painel = page.locator('dl').filter({ hasText: `${CONTA_A} → ${CONTA_B}` })
    await expect(painel.getByText('175,00', { exact: true })).toBeVisible()
    await expect(painel.getByText('0,00', { exact: true })).toBeVisible()
    await expect(painel.getByText('+175,00', { exact: true })).toBeVisible()
    await expect(
      page.getByText(`${CONTA_A} enviou R$ 175,00 a mais para ${CONTA_B} em julho.`),
    ).toBeVisible()

    // O saldo no fim do mês é o de caixa (ADR-017), somado no servidor sobre
    // TUDO o que as contas têm até 31/07: A = -50 -19,90 +1200 -33 -150 -12 -25;
    // B = +150 +25.
    const saldos = page.locator('dl').filter({ hasText: CONTA_A }).filter({ hasNotText: '→' })
    await expect(saldos.getByText('910,10', { exact: true })).toBeVisible()
    await expect(saldos.getByText('175,00', { exact: true })).toBeVisible()

    // `contraparte` sem `conta` é descartado na validação da busca, antes de
    // virar um pedido que o servidor responderia com 400: o GET /transfers
    // sai sem filtro nenhum e a tela não oferece "Outra conta". (O parâmetro
    // continua na barra de endereço, como `?mes=banana` nas outras telas —
    // ele só não vale nada.)
    const [pedido] = await Promise.all([
      page.waitForRequest((r) => r.url().includes('/transfers?') && r.method() === 'GET'),
      page.goto(`/transferencias?mes=${MES}&contraparte=${idDaContaB}`),
    ])
    const filtros = new URL(pedido.url()).searchParams
    expect(filtros.has('accountId')).toBe(false)
    expect(filtros.has('counterpartAccountId')).toBe(false)
    await expect(page.getByRole('heading', { level: 1, name: 'Transferências' })).toBeVisible()
    await expect(page.getByLabel('Outra conta', { exact: true })).toHaveCount(0)
    await expect(page.getByLabel('Conta', { exact: true }).locator('option:checked')).toHaveText(
      'Todas as contas',
    )
  })

  test('o extrato de B com as linhas espelhadas vem "já registrada", e vincular não cria lançamento', async ({
    page,
  }) => {
    await enviarExtrato(page, CONTA_B, 'NU_B.csv', EXTRATO_B)

    await expect(
      page.getByText(
        '0 precisam da sua decisão · 2 transferências detectadas · 1 prontas para importar · 0 ficam de fora.',
      ),
    ).toBeVisible()
    await expect(
      page.getByRole('rowheader', { name: /Já registrada como transferência · 2/ }),
    ).toBeVisible()
    // Sem `transferencia_interna`, o botão de aceitar não existe: `link` já resolve.
    await expect(page.getByRole('button', { name: /Aceitar/ })).toHaveCount(0)

    // A evidência traz a data da PERNA que já existe (`matchOccurredOn`): a
    // segunda linha é de 23/07, mas o par foi registrado em 22/07.
    await expect(
      page.getByText(`Já registrada em 15/07 como transferência com ${CONTA_A}.`),
    ).toBeVisible()
    await expect(
      page.getByText(`Já registrada em 22/07 como transferência com ${CONTA_A}.`),
    ).toBeVisible()

    // Default `link`, e `import` NÃO é oferecido — seria a duplicata.
    const primeira = decisaoDe(page, 'Pix recebido - ITAU QA', '15/07')
    await expect(primeira).toHaveValue('link')
    await expect(primeira.locator('option')).toHaveText([
      'Vincular à transferência já registrada',
      'Não importar',
    ])

    await expect(
      page.getByRole('button', { name: 'Importar 1 lançamento · 2 vinculados' }),
    ).toBeVisible()
    await expect(page.getByText('Inclui 2 vínculos a transferências já registradas.')).toBeVisible()

    // `link` é o default: aceitar não é exceção, e o corpo vai vazio.
    const [confirmacao] = await Promise.all([
      page.waitForRequest(
        (r) => /\/imports\/[0-9a-f-]+\/confirm$/.test(r.url()) && r.method() === 'POST',
      ),
      page.getByRole('button', { name: 'Importar 1 lançamento · 2 vinculados' }).click(),
    ])
    expect(confirmacao.postDataJSON()).toEqual({ decisions: [] })

    await expect(
      page.getByRole('heading', { level: 1, name: 'Importação concluída' }),
    ).toBeVisible()
    await expect(
      page.getByRole('status').filter({ hasText: '2 vinculadas a transferências que já existiam' }),
    ).toBeVisible()
    const vinculadas = page
      .locator('dl div')
      .filter({ hasText: 'Vinculadas a transferências já registradas' })
    await expect(vinculadas.locator('dd')).toHaveText('2')

    // Vincular não moveu dinheiro: os saldos são os mesmos de antes, e o mês
    // continua com os MESMOS dois pares — nenhum terceiro nasceu.
    await page.goto(`/transferencias?mes=${MES}&conta=${idDaContaA}&contraparte=${idDaContaB}`)
    const saldos = page.locator('dl').filter({ hasText: CONTA_A }).filter({ hasNotText: '→' })
    await expect(saldos.getByText('910,10', { exact: true })).toBeVisible()
    // B: +150 +25 -8,50 (o café entrou como despesa comum).
    await expect(saldos.getByText('166,50', { exact: true })).toBeVisible()
    await expect(page.getByRole('row', { name: /Pix enviado - NUBANK/ })).toHaveCount(2)
    await expect(page.getByRole('row', { name: /Pix recebido - ITAU QA/ })).toHaveCount(0)
  })

  test('reimportar B cai em duplicado_exato: nada entra', async ({ page }) => {
    await enviarExtrato(page, CONTA_B, 'NU_B_de_novo.csv', EXTRATO_B)

    // As duas linhas vinculadas ganharam a chave da perna existente: agora são
    // "Já importada", junto com o café — no <details> NATIVO, fechado, que é
    // o único bloco colapsado da revisão; aberto, não tem controle nenhum.
    await expect(
      page.getByText(
        '0 precisam da sua decisão · 0 transferências detectadas · 0 prontas para importar · 3 ficam de fora.',
      ),
    ).toBeVisible()
    const foraDaLista = page.getByText(/Ficam de fora · 3 linhas/)
    await expect(foraDaLista).toBeVisible()
    const tabelaDeFora = page.getByRole('table', { name: 'Linhas que não podem entrar' })
    await expect(tabelaDeFora).toBeHidden()
    await foraDaLista.click()
    await expect(tabelaDeFora).toBeVisible()
    await expect(tabelaDeFora.getByRole('row', { name: /Pix recebido - ITAU QA/ })).toHaveCount(2)
    await expect(tabelaDeFora.getByRole('row', { name: /GLIMPO QA EXEMPLO/ })).toHaveCount(1)
    await expect(page.getByText('Já importada nesta conta · lançamento idêntico.')).toHaveCount(3)
    await expect(page.getByRole('combobox')).toHaveCount(0)
    await expect(page.getByRole('checkbox')).toHaveCount(0)
    await expect(
      page.getByRole('rowheader', { name: /Já registrada como transferência/ }),
    ).toHaveCount(0)
    await expect(page.getByRole('rowheader', { name: /Parece transferência/ })).toHaveCount(0)

    const confirmar = page.getByRole('button', { name: 'Nada marcado para importar' })
    await expect(confirmar).toHaveAttribute('aria-disabled', 'true')

    await page.getByRole('button', { name: 'Cancelar importação' }).click()
    await page.getByRole('button', { name: 'Descartar o arquivo' }).click()
    await expect(page).toHaveURL(/\/importar$/)
  })
})

test.describe('categorizar automaticamente', () => {
  test('prévia, confirmação com o número real e a faixa de pendência diminuindo', async ({
    page,
  }) => {
    // Cadastra as palavras que vão casar com o que ficou sem categoria em
    // julho: VRANDIX (QA Cuidado), PLINTAQ (QA Trajeto) e KREVOL (QA Feira, que
    // já tem «zumbra» — a lista inteira volta com as duas).
    await page.goto('/categorias')
    for (const [categoria, palavra] of [
      [GRUPO_CUIDADO, 'vrandix'],
      [GRUPO_TRAJETO, 'plintaq'],
      [GRUPO_FEIRA, 'krevol'],
    ] as const) {
      await page.getByRole('button', { name: `Editar ${categoria}` }).click()
      const dialogo = page.locator('dialog[open]')
      await campoDePalavras(dialogo).fill(palavra)
      await campoDePalavras(dialogo).press('Enter')
      await expect(fichas(dialogo).filter({ hasText: palavra })).toHaveCount(1)
      await dialogo.getByRole('button', { name: 'Salvar' }).click()
      await expect(dialogo).toHaveCount(0)
    }
    await page.getByRole('button', { name: `Editar ${GRUPO_FEIRA}` }).click()
    await expect(fichas(page.locator('dialog[open]'))).toHaveText(['zumbra', 'krevol'])
    await page.keyboard.press('Escape')

    await page.goto(`/lancamentos?mes=${MES}`)
    // Julho: VRANDIX, TARVIN, PLINTAQ (conta A), KREVOL (A) e GLIMPO (B) sem
    // categoria; as pernas de transferência não contam.
    await expect(faixaDePendencia(page)).toContainText('5 lançamentos de julho estão sem categoria')

    await page.getByRole('button', { name: 'Categorizar automaticamente' }).click()
    const dialogo = page.locator('dialog[open]')
    await expect(
      dialogo.getByRole('heading', { name: 'Categorizar automaticamente' }),
    ).toBeVisible()
    await expect(dialogo.getByText(/Julho de 2026 · aplica as palavras-chave/)).toBeVisible()

    // A prévia é do servidor: 3 casam (100, sem pontuação), 2 continuam sem.
    await expect(dialogo.getByRole('status').filter({ hasText: 'recebem categoria' })).toHaveText(
      '3 de 5 recebem categoria.',
    )
    await expect(dialogo.getByRole('rowheader', { name: /Recebem categoria · 3/ })).toBeVisible()
    await expect(
      dialogo.getByRole('rowheader', { name: /Continuam sem categoria · 2/ }),
    ).toBeVisible()
    await expect(dialogo.getByRole('row', { name: /VRANDIX QA EXEMPLO/ })).toContainText(
      GRUPO_CUIDADO,
    )
    await expect(dialogo.getByRole('row', { name: /VRANDIX QA EXEMPLO/ })).toContainText(
      '«vrandix»',
    )
    await expect(dialogo.getByRole('row', { name: /PLINTAQ QA EXEMPLO/ })).toContainText(
      GRUPO_TRAJETO,
    )
    await expect(dialogo.getByRole('row', { name: /KREVOL QA EXEMPLO/ })).toContainText(
      GRUPO_FEIRA,
    )
    await expect(dialogo.getByRole('row', { name: /TARVIN QA EXEMPLO/ })).toContainText(
      'abaixo de 80%',
    )
    await expect(dialogo.getByRole('row', { name: /GLIMPO QA EXEMPLO/ })).toContainText(
      'abaixo de 80%',
    )

    // Confirmar manda `dryRun: false` para o mesmo mês.
    const [gravacao] = await Promise.all([
      page.waitForRequest(
        (r) =>
          r.url().endsWith('/transactions/auto-categorize') &&
          r.method() === 'POST' &&
          r.postDataJSON()?.dryRun === false,
      ),
      dialogo.getByRole('button', { name: 'Categorizar 3 lançamentos' }).click(),
    ])
    expect(gravacao.postDataJSON()).toEqual({ month: MES, dryRun: false })

    // O toast traz o número que o SERVIDOR devolveu, e a faixa diminui.
    await expect(page.getByText('3 lançamentos categorizados.')).toBeVisible()
    await expect(dialogo).toHaveCount(0)
    await expect(faixaDePendencia(page)).toContainText('2 lançamentos de julho estão sem categoria')
    // Desde a emenda §19 o nome da categoria é o texto de um BOTÃO (o outro
    // estado fechado do mesmo controle), e ele existe duas vezes por linha —
    // uma visível por faixa de largura, como já acontecia com a lacuna.
    await expect(
      page
        .getByRole('row', { name: /VRANDIX QA EXEMPLO/ })
        .getByRole('button', { name: /^QA Cuidado\. Trocar categoria de VRANDIX QA EXEMPLO,/ })
        .filter({ visible: true }),
    ).toHaveCount(1)
    // O que já tinha categoria não mudou.
    await expect(
      page
        .getByRole('row', { name: /ZUMBRALINO DO SEU JOSE/ })
        .getByRole('button', { name: /^QA Feira\. Trocar categoria de ZUMBRALINO DO SEU JOSE,/ })
        .filter({ visible: true }),
    ).toHaveCount(1)

    // Idempotente: rodar de novo não acha nada — e o botão explica em vez de apagar.
    await page.getByRole('button', { name: 'Categorizar automaticamente' }).click()
    const deNovo = page.locator('dialog[open]')
    await expect(deNovo.getByText('Nenhum lançamento receberia categoria.')).toBeVisible()
    const nada = deNovo.getByRole('button', { name: 'Nada a categorizar' })
    await expect(nada).toHaveAttribute('aria-disabled', 'true')
    // Nunca `disabled` (checklist 11 do DESIGN.md): continua focável, e o
    // rótulo é o que explica. (`toBeEnabled` do Playwright conta aria-disabled
    // como desabilitado, por isso a checagem é no atributo e no foco.)
    expect(await nada.evaluate((botao) => (botao as HTMLButtonElement).disabled)).toBe(false)
    await nada.focus()
    await expect(nada).toBeFocused()
    await deNovo.getByRole('button', { name: 'Cancelar' }).click()
    await expect(deNovo).toHaveCount(0)
  })
})

test.describe('palavra-chave duplicada', () => {
  test('a mesma palavra em outra categoria é 409, e a tela diz em qual ela já está', async ({
    page,
  }) => {
    await page.goto('/categorias')
    await page.getByRole('button', { name: `Editar ${GRUPO_SERVICO}` }).click()
    const dialogo = page.locator('dialog[open]')

    const input = campoDePalavras(dialogo)
    // Caixa diferente de propósito: a unicidade é pela forma normalizada.
    await input.fill('Zumbra')
    await input.press('Enter')
    await expect(fichas(dialogo)).toHaveText(['Zumbra'])

    const [resposta] = await Promise.all([
      page.waitForResponse(
        (r) => /\/categories\/[0-9a-f-]+$/.test(r.url()) && r.request().method() === 'PATCH',
      ),
      dialogo.getByRole('button', { name: 'Salvar' }).click(),
    ])
    expect(resposta.status()).toBe(409)
    const corpo = (await resposta.json()) as {
      error: { code: string; fields: Record<string, string> }
    }
    expect(corpo.error.code).toBe('KEYWORD_TAKEN')
    // A palavra recusada volta como foi enviada; a ficha é achada pela forma
    // normalizada, então caixa e acento não importam aqui.
    expect(corpo.error.fields.keyword?.toLowerCase()).toBe('zumbra')

    // A frase é nossa, com a palavra como foi digitada e a dona pelo NOME —
    // o `ownerId` nunca aparece cru. O diálogo fica aberto para corrigir.
    await expect(dialogo.getByText(`«Zumbra» já está em ${GRUPO_FEIRA}.`)).toBeVisible()
    await expect(dialogo.getByText(corpo.error.fields.ownerId ?? 'id-que-nao-existe')).toHaveCount(
      0,
    )
    await expect(input).toHaveAttribute('aria-invalid', 'true')
    // A ficha marcada carrega ícone além da borda (erro nunca só por cor)...
    const ficha = fichas(dialogo).first()
    await expect(ficha).toHaveAttribute('data-invalid', 'true')
    expect(await ficha.locator('svg').count()).toBe(2)
    // ...e continua removível — removê-la limpa o erro.
    await dialogo.getByRole('button', { name: 'Remover Zumbra' }).click()
    await expect(fichas(dialogo)).toHaveCount(0)
    await expect(dialogo.getByText(`«Zumbra» já está em ${GRUPO_FEIRA}.`)).toHaveCount(0)
    await expect(input).not.toHaveAttribute('aria-invalid')

    await dialogo.getByRole('button', { name: 'Cancelar' }).click()
    await expect(dialogo).toHaveCount(0)
  })

  test('em conta, a palavra de outra conta é 409 com a dona; categoria e conta são conjuntos independentes', async ({
    page,
  }) => {
    await page.goto('/contas')
    await page.getByRole('button', { name: `Editar ${CONTA_A}` }).click()
    const dialogo = page.locator('dialog[open]')
    await expect(fichas(dialogo)).toHaveText(['itau'])

    const input = campoDePalavras(dialogo)
    await input.fill('nubank')
    await input.press('Enter')
    await dialogo.getByRole('button', { name: 'Salvar' }).click()
    await expect(dialogo.getByText(`«nubank» já está na conta ${CONTA_B}.`)).toBeVisible()
    await dialogo.getByRole('button', { name: 'Remover nubank' }).click()

    // «zumbra» está numa CATEGORIA; em conta é outro conjunto: 2xx.
    await input.fill('zumbra')
    await input.press('Enter')
    await dialogo.getByRole('button', { name: 'Salvar' }).click()
    await expect(page.getByText('Conta atualizada.')).toBeVisible()
    await expect(dialogo).toHaveCount(0)
  })
})

test.describe('reprocessar transferências', () => {
  test('importados antes da palavra-chave, o Pix entre C e D vira um par pelo reprocessamento', async ({
    page,
  }) => {
    // As duas contas nascem SEM palavra-chave, e cada extrato entra como
    // lançamento comum: a saída de C como despesa, a entrada de D como receita.
    await criarConta(page, CONTA_C)
    await criarConta(page, CONTA_D)

    await enviarExtrato(page, CONTA_C, 'NU_C_junho.csv', EXTRATO_C, '06')
    await expect(page.getByRole('rowheader', { name: /Parece transferência/ })).toHaveCount(0)
    await page.getByRole('button', { name: 'Importar 1 lançamento' }).click()
    await expect(
      page.getByRole('heading', { level: 1, name: 'Importação concluída' }),
    ).toBeVisible()

    await enviarExtrato(page, CONTA_D, 'NU_D_junho.csv', EXTRATO_D, '06')
    await expect(page.getByRole('rowheader', { name: /Parece transferência/ })).toHaveCount(0)
    await page.getByRole('button', { name: 'Importar 1 lançamento' }).click()
    await expect(
      page.getByRole('heading', { level: 1, name: 'Importação concluída' }),
    ).toBeVisible()

    // Junho conta 300 de receita e 300 de despesa — o dinheiro só mudou de
    // conta, mas o mês não sabe disso ainda.
    await page.goto(`/lancamentos?mes=${MES_REPROCESSAR}`)
    const resumo = page.locator('p').filter({ hasText: /^Entrou/ })
    await expect(resumo).toContainText('Entrou 300,00')
    await expect(resumo).toContainText('Saiu 300,00')

    // A palavra é a da PRÓPRIA conta, cadastrada DEPOIS da importação — a
    // descrição não diz o banco de destino, então é assim que a pessoa cadastra.
    await adicionarPalavraNaConta(page, CONTA_C, 'pix enviado bruno')
    await adicionarPalavraNaConta(page, CONTA_D, 'pix recebido bruno')

    // /transferencias de junho está vazia e oferece o reprocessamento nos dois
    // lugares: no cabeçalho e no próprio vazio.
    await page.goto(`/transferencias?mes=${MES_REPROCESSAR}`)
    await expect(
      page.getByText('Nenhuma transferência entre as suas contas em junho de 2026.'),
    ).toBeVisible()
    const botoes = page.getByRole('button', { name: 'Reprocessar transferências' })
    await expect(botoes).toHaveCount(2)

    // O botão abre o diálogo JÁ pedindo a prévia (dryRun: true) do mês da tela.
    const [previa] = await Promise.all([
      page.waitForRequest(
        (r) =>
          r.url().endsWith('/transfers/detect') &&
          r.method() === 'POST' &&
          r.postDataJSON()?.dryRun === true,
      ),
      botoes.first().click(),
    ])
    expect(previa.postDataJSON()).toEqual({ month: MES_REPROCESSAR, dryRun: true })

    const dialogo = page.locator('dialog[open]')
    await expect(
      dialogo.getByRole('heading', { name: 'Reprocessar transferências' }),
    ).toBeVisible()
    await expect(dialogo.getByText(/Junho de 2026 · junta receita e despesa/)).toBeVisible()

    // A prévia é do servidor: 1 par, nenhuma sem par. Data, de → para em
    // palavras, valor NEUTRO sem sinal, descrição e a palavra que decidiu
    // (100 — sem pontuação; qual das duas pernas decidiu é escolha do servidor).
    await expect(dialogo.getByRole('status')).toHaveText('1 par vira transferência.')
    await expect(dialogo.getByRole('rowheader', { name: /Viram transferência · 1/ })).toBeVisible()
    await expect(dialogo.getByRole('rowheader', { name: /não têm par/ })).toHaveCount(0)
    const linha = dialogo.getByRole('row', { name: /Pix enviado - BRUNO/ })
    await expect(linha).toBeVisible()
    await expect(linha).toContainText('10/06')
    await expect(linha.getByText(`de ${CONTA_C} para ${CONTA_D}`).first()).toBeAttached()
    await expect(linha.getByText('300,00', { exact: true })).toBeVisible()
    await expect(linha.getByText('-300,00')).toHaveCount(0)
    await expect(linha).toContainText(/«pix (enviado|recebido) bruno»/)
    await expect(linha.getByText(/100%/)).toHaveCount(0)
    expect(await dialogo.locator('[data-tone="positivo"], [data-tone="negativo"]').count()).toBe(
      0,
    )

    // Confirmar manda `dryRun: false` para o mesmo mês, sem ids no corpo: o
    // cliente não escolhe o que converter.
    const [gravacao] = await Promise.all([
      page.waitForRequest(
        (r) =>
          r.url().endsWith('/transfers/detect') &&
          r.method() === 'POST' &&
          r.postDataJSON()?.dryRun === false,
      ),
      dialogo.getByRole('button', { name: 'Converter 1 par' }).click(),
    ])
    expect(gravacao.postDataJSON()).toEqual({ month: MES_REPROCESSAR, dryRun: false })

    // O toast traz o número que o SERVIDOR devolveu; o diálogo fecha; e a
    // tabela — invalidada — passa a listar o par, com a direção em palavras.
    await expect(page.getByText('1 transferência reconhecida.')).toBeVisible()
    await expect(dialogo).toHaveCount(0)
    await expect(
      page.getByRole('link', { name: `Ver as transferências entre ${CONTA_C} e ${CONTA_D}` }),
    ).toBeVisible()
    await expect(page.getByRole('row', { name: /Pix enviado - BRUNO/ })).toHaveCount(1)
    await expect(page.getByText(`de ${CONTA_C} para ${CONTA_D}`).first()).toBeAttached()
    await expect(page.getByText('1 transferência — é tudo o que existe no filtro.')).toBeVisible()

    // O `summary` de /lancamentos não conta mais os 300,00: transferência
    // nunca entra em receita nem em despesa (ADR-016, reafirmado no ADR-028).
    await page.goto(`/lancamentos?mes=${MES_REPROCESSAR}`)
    const resumoDepois = page.locator('p').filter({ hasText: /^Entrou/ })
    await expect(resumoDepois).toContainText('Entrou 0,00')
    await expect(resumoDepois).toContainText('Saiu 0,00')

    // Idempotente: reprocessar de novo não acha nada — e o botão explica em
    // vez de sumir (nunca `disabled`: continua focável).
    await page.goto(`/transferencias?mes=${MES_REPROCESSAR}`)
    await page.getByRole('button', { name: 'Reprocessar transferências' }).first().click()
    const deNovo = page.locator('dialog[open]')
    await expect(deNovo.getByText('Nenhum par para reconhecer em junho de 2026.')).toBeVisible()
    const nada = deNovo.getByRole('button', { name: 'Nada a converter' })
    await expect(nada).toHaveAttribute('aria-disabled', 'true')
    expect(await nada.evaluate((botao) => (botao as HTMLButtonElement).disabled)).toBe(false)

    // Escape fecha e devolve o foco ao botão que abriu.
    await page.keyboard.press('Escape')
    await expect(deNovo).toHaveCount(0)
    await expect(
      page.getByRole('button', { name: 'Reprocessar transferências' }).first(),
    ).toBeFocused()
  })
})
