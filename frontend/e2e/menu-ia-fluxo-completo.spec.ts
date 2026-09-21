import { expect, type Locator, type Page, test } from '@playwright/test'

/** Ponta a ponta da ENTREGA INTEIRA do menu IA (spec 0010, critério 53 —
 *  fatias E9a + E9b + E9c), contra a API Go real, SQLite real e o Vite, na
 *  casa compartilhada do projeto `setup`:
 *
 *  1. duas contas com extratos importados ANTES de qualquer palavra-chave —
 *     o mesmo Pix visto dos dois lados entra como despesa numa e receita na
 *     outra, e as compras com nome reconhecível entram sem categoria;
 *  2. **Exportar** 3 meses → o prompt tem as nove seções, os nomes das duas
 *     contas e as descrições recém-importadas;
 *  3. **Importar** um JSON fixo (a "resposta da IA"): uma categoria nova num
 *     grupo existente, uma categoria nova num grupo novo, palavras de
 *     categoria para uma existente e palavras de conta para as duas contas
 *     (o nome de uma, vista do extrato da outra);
 *  4. prévia: bloco A com as duas marcadas, bloco B com o impacto medido POR
 *     PALAVRA, bloco C; desmarcar uma categoria derruba o rótulo do botão;
 *  5. **Confirmar** → o relatório com os números do servidor;
 *  6. **Reprocessar** → `Conferir` mostra o consolidado; `Reprocessar` chama
 *     `detect ×3` e depois `categorize ×3` (a ordem é lida na REDE, não no
 *     estado do React); o `<output>` registra o progresso;
 *  7. o estado final nas OUTRAS telas: a categoria desmarcada não existe em
 *     `/categorias` — e a palavra dela não entrou em lugar nenhum, porque o
 *     lançamento que ela reconheceria segue SEM categoria depois do
 *     reprocessamento; a criada existe com a palavra; o par virou
 *     transferência em `/transferencias` (com o "de X para Y"); as compras
 *     reconhecíveis têm categoria em `/lancamentos`; e `Conferir de novo`
 *     devolve zero (idempotência).
 *
 *  A janela atravessa a VIRADA DO ANO de propósito (dezembro de 2025, janeiro
 *  e fevereiro de 2026): é a borda que uma derivação de mês por `Date` erra, e
 *  são três meses que nenhum outro spec usa — o reprocessamento só enxerga o
 *  que este spec gravou.
 *
 *  Nomes de fixture (`QUORBIX`, `TRELNAV`, `MOSKARP`, `FIBLUNTO`, `DRAVEKO`)
 *  conferidos contra o motor real (`internal/textmatch` + a semente de
 *  `internal/category/seed.go` + as palavras que os outros specs cadastram) em
 *  21/09/2026: nenhum alcança 80 em nenhum lado do dinheiro. O spec ainda
 *  afirma isso em runtime, na revisão da importação ("Sem categoria" e zero
 *  "Parece transferência"). */

const CONTA_Q = 'Banco Quorbix'
const CONTA_T = 'Banco Trelnav'
const GRUPO_LOJA = 'QA E9 Loja' // grupo próprio sem filhas: dono de palavra de categoria
const GRUPO_EXISTENTE = 'Alimentação' // da semente, com filhas: a folha nova herda a natureza
const FOLHA_DESMARCADA = 'QA E9 Feira'
const GRUPO_NOVO = 'QA E9 Grupo Novo'
const FOLHA_CRIADA = 'QA E9 Folha Nova'

/** O mês da casca é o ÚLTIMO da janela; a janela de 3 anda para trás. */
const MES_FINAL = '2026-02'
const MESES = ['2025-12', '2026-01', '2026-02'] as const

type Linha = { data: string; valor: string; id: string; descricao: string }

/** Extrato no formato do Nubank (`Data,Valor,Identificador,Descrição`;
 *  negativo é saída). Datas completas: o arquivo cobre três meses. O prefixo do
 *  identificador é próprio deste spec — é a chave natural da deduplicação. */
function extratoCSV(linhas: readonly Linha[]): Buffer {
  const cabecalho = 'Data,Valor,Identificador,Descrição\n'
  const corpo = linhas
    .map((l) => `${l.data},${l.valor},77777777-7777-4777-8777-7777777777${l.id},${l.descricao}\n`)
    .join('')
  return Buffer.from(cabecalho + corpo, 'utf-8')
}

/** Quorbix: a saída do Pix espelhado (dez), uma compra reconhecível (dez), um
 *  Pix SEM espelho (jan) e outra compra reconhecível (fev). */
const EXTRATO_Q: readonly Linha[] = [
  { data: '10/12/2025', valor: '-250.00', id: '01', descricao: 'Pix enviado - TRELNAV' },
  { data: '12/12/2025', valor: '-40.00', id: '02', descricao: 'MOSKARP LOJA CENTRO' },
  { data: '14/01/2026', valor: '-90.00', id: '03', descricao: 'Pix enviado - TRELNAV' },
  { data: '16/02/2026', valor: '-25.50', id: '04', descricao: 'MOSKARP LOJA NORTE' },
]

/** Trelnav: a entrada espelhada (dez), uma despesa que só a categoria
 *  DESMARCADA reconheceria (jan) e uma receita que a categoria CRIADA reconhece
 *  (fev). */
const EXTRATO_T: readonly Linha[] = [
  { data: '10/12/2025', valor: '250.00', id: '11', descricao: 'Pix recebido - QUORBIX' },
  { data: '15/01/2026', valor: '-30.00', id: '12', descricao: 'FIBLUNTO MENSAL' },
  { data: '20/02/2026', valor: '500.00', id: '13', descricao: 'DRAVEKO DEPOSITO' },
]

/** As nove seções normativas da §3.1, na ordem. */
const SECOES = [
  '## 1. Papel e contexto',
  '## 2. Como o motor casa a palavra com a descrição',
  '## 3. Palavra-chave de categoria não é palavra-chave de conta',
  '## 4. Regras que o seu JSON precisa respeitar',
  '## 5. Quando criar categoria nova',
  '## 6. Contas ativas',
  '## 7. Categorias ativas',
  '## 8. Movimentações do período, agrupadas por descrição',
  '## 9. Tarefa e formato de saída',
]

type CategoriaJSON = { id: string; name: string; children?: CategoriaJSON[] }
type ArvoreJSON = Record<'expense' | 'income' | 'investment' | 'redemption', CategoriaJSON[]>
type ContasJSON = { items: Array<{ id: string; name: string; balanceCents: number }> }

// ------------------------------------------------------------- utilitários

function campoDePalavras(escopo: Page | Locator): Locator {
  return escopo.getByLabel('Palavras-chave', { exact: true })
}

function fichas(escopo: Page | Locator): Locator {
  return escopo.getByRole('list', { name: 'Palavras-chave adicionadas' }).getByRole('listitem')
}

async function criarGrupoDeDespesa(page: Page, nome: string): Promise<void> {
  await page.goto('/categorias')
  await page.getByRole('button', { name: 'Novo grupo', exact: true }).click()
  const dialogo = page.locator('dialog[open]')
  await dialogo.getByLabel('Nome').fill(nome)
  await dialogo.getByLabel('Natureza').selectOption('expense')
  await dialogo.getByRole('button', { name: /^Criar/ }).click()
  await expect(dialogo).toHaveCount(0)
  await expect(
    page.getByRole('region', { name: 'Despesas' }).getByText(nome, { exact: true }),
  ).toBeVisible()
}

async function criarConta(page: Page, nome: string): Promise<void> {
  await page.goto('/contas')
  await page.getByRole('button', { name: 'Nova conta', exact: true }).click()
  const dialogo = page.getByRole('dialog')
  await expect(dialogo.getByRole('heading', { name: 'Nova conta' })).toBeVisible()
  await dialogo.getByLabel('Nome').fill(nome)
  await dialogo.getByLabel('Tipo').selectOption('checking')
  await dialogo.getByRole('button', { name: 'Criar conta' }).click()
  await expect(page.getByRole('row', { name: new RegExp(nome) })).toBeVisible()
}

/** Importa um extrato pelas telas e exige que NADA pareça transferência e que
 *  a linha indicada entre "Sem categoria" — a prova em runtime de que as
 *  fixtures não colidem com nenhuma palavra da casa. */
async function importarExtrato(
  page: Page,
  conta: string,
  nome: string,
  linhas: readonly Linha[],
  semCategoria: { descricao: string; data: string },
): Promise<void> {
  await page.goto('/importar')
  await expect(
    page.getByRole('heading', { level: 1, name: 'Importar extrato ou fatura' }),
  ).toBeVisible()
  await page.getByLabel('Conta de destino').selectOption({ label: conta })
  await page.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
    name: nome,
    mimeType: 'text/csv',
    buffer: extratoCSV(linhas),
  })
  await page.getByRole('button', { name: 'Analisar arquivo' }).click()
  await expect(
    page.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
  ).toBeVisible()

  await expect(page.getByText(/0 transferências detectadas/)).toBeVisible()
  await expect(page.getByRole('rowheader', { name: /Parece transferência/ })).toHaveCount(0)
  await expect(
    page
      .getByRole('combobox', {
        name: new RegExp(`^Categoria de ${semCategoria.descricao}, ${semCategoria.data},`),
      })
      .locator('option:checked'),
  ).toHaveText('Sem categoria')

  await page
    .getByRole('button', { name: new RegExp(`^Importar ${linhas.length} lançamentos$`) })
    .click()
  await expect(page.getByRole('heading', { level: 1, name: 'Importação concluída' })).toBeVisible()
  await expect(
    page.getByRole('status').filter({ hasText: `${linhas.length} lançamentos importados` }),
  ).toBeVisible()
}

async function palavrasNoDialogoDaCategoria(page: Page, nome: string): Promise<string[]> {
  await page.goto('/categorias')
  await page.getByRole('button', { name: `Editar ${nome}`, exact: true }).click()
  const dialogo = page.locator('dialog[open]')
  await expect(dialogo.getByRole('heading', { name: 'Editar categoria' })).toBeVisible()
  await expect(campoDePalavras(dialogo)).toBeVisible()
  const textos = await fichas(dialogo).allTextContents()
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  return textos
}

async function palavrasNoDialogoDaConta(page: Page, nome: string): Promise<string[]> {
  await page.goto('/contas')
  await page.getByRole('button', { name: `Editar ${nome}` }).click()
  const dialogo = page.locator('dialog[open]')
  await expect(dialogo.getByRole('heading', { name: 'Editar conta' })).toBeVisible()
  const textos = await fichas(dialogo).allTextContents()
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  return textos
}

function categoriaNaArvore(arvore: ArvoreJSON, nome: string): CategoriaJSON | undefined {
  for (const natureza of Object.values(arvore)) {
    for (const grupo of natureza) {
      if (grupo.name === nome) return grupo
      const folha = grupo.children?.find((c) => c.name === nome)
      if (folha) return folha
    }
  }
  return undefined
}

async function arvore(page: Page): Promise<ArvoreJSON> {
  return (await (await page.request.get('/api/v1/categories')).json()) as ArvoreJSON
}

async function contas(page: Page): Promise<ContasJSON> {
  return (await (await page.request.get('/api/v1/accounts')).json()) as ContasJSON
}

/** O botão de categoria de uma linha de `/lancamentos`, no estado visível da
 *  faixa de largura atual (o controle existe duas vezes por linha). */
function botaoDeCategoria(page: Page, descricao: string, categoria: string | null): Locator {
  const nome =
    categoria === null
      ? new RegExp(`^Sem categoria\\. Categorizar ${descricao},`)
      : new RegExp(`^${categoria}\\. Trocar categoria de ${descricao},`)
  return page
    .getByRole('row', { name: new RegExp(descricao) })
    .getByRole('button', { name: nome })
    .filter({ visible: true })
}

type Chamada = { rota: 'detect' | 'categorize'; month: string; dryRun: boolean }

test.describe.configure({ mode: 'serial' })

test.describe('menu IA — a entrega inteira, ponta a ponta', () => {
  test.beforeEach(({ page }) => {
    page.on('pageerror', (erro) => {
      throw new Error(`erro de página não tratado: ${erro.message}
${erro.stack ?? ''}`)
    })
    page.on('console', (msg) => {
      if (msg.type() === 'error') console.log(`[console.error] ${msg.text()}`)
    })
  })

  test('importar antes das palavras → exportar → importar o JSON → desmarcar → confirmar → reprocessar → conferir nas outras telas', async ({
    page,
  }) => {
    // Sete idas e vindas de tela mais duas importações e um reprocessamento
    // de seis chamadas: o prazo padrão de 60 s é apertado demais para o fluxo
    // inteiro num só teste — e o fluxo É um só.
    test.setTimeout(180_000)

    // === 1) a casa: duas contas, um grupo sem filhas, extratos SEM palavra ===
    await criarConta(page, CONTA_Q)
    await criarConta(page, CONTA_T)
    await criarGrupoDeDespesa(page, GRUPO_LOJA)

    await importarExtrato(page, CONTA_Q, 'quorbix.csv', EXTRATO_Q, {
      descricao: 'MOSKARP LOJA CENTRO',
      data: '12/12',
    })
    await importarExtrato(page, CONTA_T, 'trelnav.csv', EXTRATO_T, {
      descricao: 'DRAVEKO DEPOSITO',
      data: '20/02',
    })

    const contasAntes = await contas(page)
    const contaQ = contasAntes.items.find((c) => c.name === CONTA_Q)
    const contaT = contasAntes.items.find((c) => c.name === CONTA_T)
    expect(contaQ, 'a conta Quorbix existe').toBeDefined()
    expect(contaT, 'a conta Trelnav existe').toBeDefined()
    expect(contaQ?.balanceCents).toBe(-40550)
    expect(contaT?.balanceCents).toBe(72000)

    const arvoreAntes = await arvore(page)
    const loja = categoriaNaArvore(arvoreAntes, GRUPO_LOJA)
    const alimentacao = arvoreAntes.expense.find((c) => c.name === GRUPO_EXISTENTE)
    expect(loja, 'o grupo dono das palavras de categoria existe').toBeDefined()
    expect(alimentacao, 'o grupo existente da semente existe').toBeDefined()
    expect(alimentacao?.children?.length ?? 0, 'e tem filhas: a folha nova herda a natureza').toBeGreaterThan(0)

    // Dezembro conta o Pix como receita E despesa: o mês ainda não sabe que o
    // dinheiro só mudou de conta.
    await page.goto(`/lancamentos?mes=${MESES[0]}`)
    const resumoAntes = page.locator('p').filter({ hasText: /^Entrou/ })
    await expect(resumoAntes).toContainText('Entrou 250,00')
    await expect(resumoAntes).toContainText('Saiu 290,00')

    // === 2) exportar: a janela de três meses, na virada do ano ==============
    const urlsDeExportacao: string[] = []
    page.on('requestfinished', (req) => {
      if (req.url().includes('/api/v1/ai/export-prompt')) urlsDeExportacao.push(req.url())
    })
    await page.goto(`/ia?mes=${MES_FINAL}`)
    await expect(page.getByRole('heading', { level: 1, name: 'IA' })).toBeVisible()
    await expect(page.getByLabel('Período')).toHaveValue('3')
    await expect(page.getByLabel('Período').locator('option:checked')).toHaveText(
      '3 meses · dezembro a fevereiro',
    )
    await expect.poll(() => urlsDeExportacao.length).toBe(1)
    const pedida = new URL(urlsDeExportacao[0] as string)
    expect(pedida.searchParams.get('fromMonth')).toBe(MESES[0])
    expect(pedida.searchParams.get('toMonth')).toBe(MESES[2])

    await page.locator('summary').first().click()
    const caixa = page.getByLabel('Texto do prompt')
    await expect(caixa).toBeVisible()
    const prompt = await caixa.locator('pre').evaluate((el) => el.textContent ?? '')
    let anterior = -1
    for (const titulo of SECOES) {
      const pos = prompt.indexOf(titulo)
      expect(pos, `seção ausente: ${titulo}`).toBeGreaterThan(-1)
      expect(pos, `seção fora de ordem: ${titulo}`).toBeGreaterThan(anterior)
      anterior = pos
    }
    // As duas contas, com id, nome e tipo — e o id é o mesmo que a API dá,
    // porque é a chave de volta do JSON.
    expect(prompt).toContain(`| ${contaQ?.id} | ${CONTA_Q} | checking |`)
    expect(prompt).toContain(`| ${contaT?.id} | ${CONTA_T} | checking |`)
    // As movimentações recém-importadas estão na seção 8, agrupadas: o Pix
    // enviado aparece UMA vez com duas ocorrências.
    expect(prompt).toContain('| Pix enviado - TRELNAV | 2 |')
    expect(prompt).toContain('| MOSKARP LOJA CENTRO | 1 |')
    expect(prompt).toContain('| DRAVEKO DEPOSITO | 1 |')
    expect(prompt).toContain(`| ${loja?.id} | ${GRUPO_LOJA} | expense |`)

    // === 3) importar: o JSON "da IA" ========================================
    const payload = {
      homefinanceKeywordImport: 1,
      newCategories: [
        // Grupo existente: sem `kind`, a natureza é herdada. É esta que vai
        // ser DESMARCADA — e «fiblunto» é a palavra que não pode entrar.
        { group: GRUPO_EXISTENTE, name: FOLHA_DESMARCADA, add: ['fiblunto'] },
        // Grupo novo: `kind` obrigatório. Receita, para «draveko» reconhecer
        // o DEPÓSITO no reprocessamento.
        { group: GRUPO_NOVO, name: FOLHA_CRIADA, kind: 'income', add: ['draveko'] },
      ],
      categoryKeywords: [{ categoryId: loja?.id, categoryPath: GRUPO_LOJA, add: ['moskarp'] }],
      accountKeywords: [
        // O nome de UMA conta, visto do extrato da OUTRA.
        { accountId: contaT?.id, accountName: CONTA_T, add: ['trelnav'] },
        { accountId: contaQ?.id, accountName: CONTA_Q, add: ['quorbix'] },
      ],
      notes: 'a IA explicando o que fez',
    }

    const corpos: Array<{ rota: string; corpo: { payload: unknown; skipNewCategories?: string[] } }> =
      []
    page.on('request', (req) => {
      if (req.url().includes('/api/v1/ai/keyword-import/') && req.method() === 'POST') {
        corpos.push({ rota: req.url().split('/api/v1')[1] ?? '', corpo: req.postDataJSON() })
      }
    })

    // Escopado à seção 2: desde a E9c a seção 3 também tem um botão "Conferir"
    // na página, e `page.getByRole` sozinho resolve para dois elementos.
    const importar = page.getByRole('region', { name: '2 · Importar o que a IA respondeu' })
    await importar
      .getByLabel('Cole aqui o JSON que a IA respondeu')
      .fill(JSON.stringify(payload, null, 2))
    await importar.getByRole('button', { name: 'Conferir', exact: true }).click()

    // === 4) a prévia: A com as duas marcadas, B com o impacto, C ============
    await expect(page.getByRole('heading', { name: 'Categorias a criar · 2' })).toBeVisible()
    const caixaDesmarcada = page.getByRole('checkbox', {
      name: `Criar ${GRUPO_EXISTENTE} > ${FOLHA_DESMARCADA} com 1 palavra-chave`,
    })
    const caixaCriada = page.getByRole('checkbox', {
      name: `Criar ${GRUPO_NOVO} > ${FOLHA_CRIADA} com 1 palavra-chave`,
    })
    await expect(caixaDesmarcada).toBeChecked()
    await expect(caixaCriada).toBeChecked()

    // Bloco B: uma linha por palavra, com o impacto MEDIDO sobre os 7
    // lançamentos vivos da janela: «trelnav» casa com os dois Pix enviados,
    // «quorbix» com o único Pix recebido.
    await expect(page.getByRole('heading', { name: 'Palavras-chave de conta · 2' })).toBeVisible()
    const tabelaB = page.getByRole('table', {
      name: 'Palavras-chave de conta e o impacto no período',
    })
    await expect(tabelaB.getByRole('row', { name: /«trelnav»/ })).toContainText(
      '2 de 7 candidatos a transferência',
    )
    await expect(tabelaB.getByRole('row', { name: /«quorbix»/ })).toContainText(
      '1 de 7 candidatos a transferência',
    )

    await expect(
      page.getByRole('heading', { name: 'Palavras-chave de categoria · 1' }),
    ).toBeVisible()

    // Totais e rótulo com tudo marcado: 2 categorias, 1 + 1 + 1 + 2 = 5 palavras.
    const status = page.getByRole('status').filter({ hasText: /palavras entram/ })
    await expect(status).toHaveText('2 categorias novas · 5 palavras entram.')
    await expect(
      page.getByRole('button', { name: 'Criar 2 categorias e gravar 5 palavras' }),
    ).toBeVisible()

    // A prévia não gravou nada.
    const arvoreDaPrevia = await arvore(page)
    expect(categoriaNaArvore(arvoreDaPrevia, FOLHA_DESMARCADA)).toBeUndefined()
    expect(categoriaNaArvore(arvoreDaPrevia, FOLHA_CRIADA)).toBeUndefined()
    expect(categoriaNaArvore(arvoreDaPrevia, GRUPO_NOVO)).toBeUndefined()

    // Desmarcar UMA: o rótulo cai 1 categoria e 1 palavra.
    await caixaDesmarcada.uncheck()
    await expect(caixaDesmarcada).not.toBeChecked()
    await expect(caixaCriada).toBeChecked()
    await expect(status).toHaveText('1 categoria nova · 4 palavras entram.')
    const confirmar = page.getByRole('button', { name: 'Criar 1 categoria e gravar 4 palavras' })
    await expect(confirmar).toBeVisible()

    // === 5) confirmar: os números do servidor ================================
    await confirmar.click()
    await expect(page.getByRole('status').filter({ hasText: /gravadas?\./ })).toHaveText(
      '1 categoria criada e 4 palavras gravadas.',
    )
    await expect(page.getByText('Desmarcadas por você')).toBeVisible()

    expect(corpos.map((c) => c.rota)).toEqual([
      '/ai/keyword-import/preview',
      '/ai/keyword-import/confirm',
    ])
    expect(corpos[1]?.corpo.payload).toEqual(corpos[0]?.corpo.payload)
    expect(corpos[0]?.corpo.skipNewCategories ?? []).toEqual([])
    expect(corpos[1]?.corpo.skipNewCategories).toEqual(['alimentacao > qa e9 feira'])

    // Gravar palavra-chave, sozinho, NÃO mexe em lançamento nenhum: dezembro
    // continua contando o Pix como receita e despesa.
    const dezembroAntes = (await (
      await page.request.get(`/api/v1/transactions?month=${MESES[0]}&limit=100`)
    ).json()) as { items: Array<{ kind: string; categoryId: string | null }> }
    expect(dezembroAntes.items.map((i) => i.kind).sort()).toEqual(['expense', 'expense', 'income'])
    expect(dezembroAntes.items.every((i) => i.categoryId === null)).toBe(true)

    // === 6) reprocessar =====================================================
    const chamadas: Chamada[] = []
    page.on('request', (req) => {
      if (req.method() !== 'POST') return
      const url = req.url()
      const rota = url.endsWith('/transfers/detect')
        ? 'detect'
        : url.endsWith('/transactions/auto-categorize')
          ? 'categorize'
          : null
      if (!rota) return
      const corpo = req.postDataJSON() as { month: string; dryRun: boolean }
      chamadas.push({ rota, month: corpo.month, dryRun: corpo.dryRun })
    })

    // "Ir para Reprocessar" leva o FOCO ao título da seção 3.
    await page.getByRole('button', { name: 'Ir para Reprocessar' }).click()
    const titulo = page.getByRole('heading', { level: 2, name: '3 · Reprocessar' })
    await expect(titulo).toBeFocused()
    const secao = page.getByRole('region', { name: '3 · Reprocessar' })
    await expect(secao).toContainText('Dezembro, janeiro e fevereiro.')

    // Conferir: seis prévias, o consolidado é a SOMA das respostas.
    await secao.getByRole('button', { name: 'Conferir', exact: true }).click()
    const reprocessar = secao.getByRole('button', {
      name: 'Reprocessar dezembro, janeiro e fevereiro',
    })
    await expect(reprocessar).toBeVisible()
    await expect.poll(() => chamadas.length).toBe(6)
    expect(chamadas.every((c) => c.dryRun)).toBe(true)
    for (const mes of MESES) {
      expect(chamadas).toContainEqual({ rota: 'detect', month: mes, dryRun: true })
      expect(chamadas).toContainEqual({ rota: 'categorize', month: mes, dryRun: true })
    }

    await expect(secao.getByRole('status')).toHaveText(
      '1 par de transferência · 3 lançamentos categorizados · 4 seguem sem categoria.',
    )
    const previa = secao.getByRole('table', { name: 'Prévia do reprocessamento por mês' })
    await expect(previa.getByRole('row')).toHaveText([
      /Mês/,
      'Dezembro de 2025112',
      'Janeiro de 2026002',
      'Fevereiro de 2026020',
    ])
    // O Pix de janeiro não tem a outra perna gravada: sem par, com o motivo,
    // e a direção diz qual extrato falta.
    const semPar = secao.getByText('Ver o lançamento sem par e o motivo')
    await expect(semPar).toBeVisible()
    await semPar.click()
    const tabelaSemPar = secao.getByRole('table', {
      name: 'Lançamentos que parecem transferência, mas não têm par',
    })
    await expect(tabelaSemPar.getByRole('row', { name: /Pix enviado - TRELNAV/ })).toContainText(
      'sem a outra perna gravada',
    )
    await expect(tabelaSemPar.getByRole('row', { name: /Pix enviado - TRELNAV/ })).toContainText(
      `saiu de ${CONTA_Q}`,
    )

    // Reprocessar: fase 1 em todos os meses, fase 2 em todos, em SEQUÊNCIA.
    await reprocessar.click()
    const saida = secao.locator('output')
    await expect(saida).toHaveText(
      [
        'Dezembro: 1 par.',
        'Janeiro: 0 pares.',
        'Fevereiro: 0 pares.',
        'Dezembro: 1 categorizado.',
        'Janeiro: 0 categorizados.',
        'Fevereiro: 2 categorizados.',
        'Reprocessado — 1 par de transferência e 3 lançamentos categorizados.',
      ].join(''),
    )
    await expect(saida).toHaveAttribute('aria-live', 'polite')

    expect(chamadas.slice(6)).toEqual([
      { rota: 'detect', month: '2025-12', dryRun: false },
      { rota: 'detect', month: '2026-01', dryRun: false },
      { rota: 'detect', month: '2026-02', dryRun: false },
      { rota: 'categorize', month: '2025-12', dryRun: false },
      { rota: 'categorize', month: '2026-01', dryRun: false },
      { rota: 'categorize', month: '2026-02', dryRun: false },
    ])
    expect(chamadas).toHaveLength(12)

    const andamento = secao.getByRole('table', { name: 'Andamento do reprocessamento por mês' })
    await expect(andamento.getByText('feito')).toHaveCount(6)
    const lista = secao.locator('dl')
    await expect(lista).toContainText('Pares de transferência1')
    await expect(lista).toContainText('Lançamentos categorizados3')
    await expect(secao.getByRole('link', { name: 'Ver as transferências' })).toHaveAttribute(
      'href',
      `/transferencias?mes=${MES_FINAL}`,
    )
    await expect(secao.getByRole('link', { name: 'Ver os lançamentos' })).toHaveAttribute(
      'href',
      `/lancamentos?mes=${MES_FINAL}`,
    )
    await expect(secao.getByRole('alert')).toHaveCount(0)

    // Conferir de novo, ainda na mesma página: tudo volta com 0 — a prova de
    // que está feito. Os sem categoria que sobram são os que nenhuma palavra
    // reconhece (o Pix de janeiro sem par e o FIBLUNTO da categoria desmarcada).
    await secao.getByRole('button', { name: 'Conferir de novo' }).click()
    const nada = secao.getByRole('button', { name: 'Nada a reprocessar' })
    await expect(nada).toBeVisible()
    await expect(nada).toHaveAttribute('aria-disabled', 'true')
    await expect(nada).not.toHaveAttribute('disabled')
    await expect(secao.getByRole('status')).toHaveText(
      'Nada a reprocessar — não há par para reconhecer, e nenhuma palavra-chave bate com os 2 lançamentos sem categoria.',
    )
    await expect.poll(() => chamadas.length).toBe(18)
    expect(chamadas.slice(12).every((c) => c.dryRun)).toBe(true)

    // === 7) o estado final nas OUTRAS telas =================================

    // /categorias: a desmarcada não existe; a criada existe, no grupo novo de
    // RECEITA, com a palavra dela; o grupo dono recebeu a palavra de categoria.
    await page.goto('/categorias')
    await expect(page.getByText(FOLHA_DESMARCADA, { exact: true })).toHaveCount(0)
    const receitas = page.getByRole('region', { name: 'Receitas' })
    await expect(receitas.getByText(GRUPO_NOVO, { exact: true })).toBeVisible()
    await expect(receitas.getByText(FOLHA_CRIADA, { exact: true })).toBeVisible()
    expect(await palavrasNoDialogoDaCategoria(page, FOLHA_CRIADA)).toEqual(['draveko'])
    expect(await palavrasNoDialogoDaCategoria(page, GRUPO_LOJA)).toEqual(['moskarp'])
    const arvoreFinal = await arvore(page)
    expect(categoriaNaArvore(arvoreFinal, FOLHA_DESMARCADA)).toBeUndefined()
    expect(JSON.stringify(arvoreFinal)).not.toContain('fiblunto')
    // O grupo existente continua com as mesmas filhas de antes.
    expect(
      arvoreFinal.expense.find((c) => c.name === GRUPO_EXISTENTE)?.children?.length,
    ).toBe(alimentacao?.children?.length)

    // /contas: cada conta tem a palavra que a OUTRA reconhece.
    expect(await palavrasNoDialogoDaConta(page, CONTA_T)).toEqual(['trelnav'])
    expect(await palavrasNoDialogoDaConta(page, CONTA_Q)).toEqual(['quorbix'])

    // /transferencias: o par de dezembro, com a direção em palavras.
    await page.goto(`/transferencias?mes=${MESES[0]}`)
    const linhaDoPar = page.getByRole('row', { name: /Pix enviado - TRELNAV/ })
    await expect(linhaDoPar).toHaveCount(1)
    await expect(linhaDoPar).toContainText('10/12')
    await expect(page.getByText(`de ${CONTA_Q} para ${CONTA_T}`).first()).toBeAttached()
    await expect(page.getByText('1 transferência — é tudo o que existe no filtro.')).toBeVisible()
    // Janeiro: o Pix sem par NÃO virou transferência — nenhuma perna inventada.
    await page.goto(`/transferencias?mes=${MESES[1]}`)
    await expect(
      page.getByText('Nenhuma transferência entre as suas contas em janeiro de 2026.'),
    ).toBeVisible()

    // /lancamentos: as compras reconhecíveis têm categoria; o Pix de dezembro
    // saiu do resumo (transferência não é receita nem despesa); o FIBLUNTO
    // segue sem categoria — a palavra da desmarcada não entrou.
    await page.goto(`/lancamentos?mes=${MESES[0]}`)
    await expect(botaoDeCategoria(page, 'MOSKARP LOJA CENTRO', GRUPO_LOJA)).toHaveCount(1)
    const resumoDepois = page.locator('p').filter({ hasText: /^Entrou/ })
    await expect(resumoDepois).toContainText('Entrou 0,00')
    await expect(resumoDepois).toContainText('Saiu 40,00')

    await page.goto(`/lancamentos?mes=${MESES[1]}`)
    await expect(botaoDeCategoria(page, 'FIBLUNTO MENSAL', null)).toHaveCount(1)
    await expect(botaoDeCategoria(page, 'Pix enviado - TRELNAV', null)).toHaveCount(1)

    await page.goto(`/lancamentos?mes=${MESES[2]}`)
    await expect(botaoDeCategoria(page, 'MOSKARP LOJA NORTE', GRUPO_LOJA)).toHaveCount(1)
    await expect(botaoDeCategoria(page, 'DRAVEKO DEPOSITO', FOLHA_CRIADA)).toHaveCount(1)

    // O saldo das duas contas não mudou com a conversão do par (aceite 40).
    const contasDepois = await contas(page)
    expect(contasDepois.items.find((c) => c.name === CONTA_Q)?.balanceCents).toBe(
      contaQ?.balanceCents,
    )
    expect(contasDepois.items.find((c) => c.name === CONTA_T)?.balanceCents).toBe(
      contaT?.balanceCents,
    )

    // E o par está gravado como par: as duas pernas com o mesmo grupo e sem
    // categoria, apesar de «trelnav» e «quorbix» reconhecerem as duas.
    const dezembro = (await (
      await page.request.get(`/api/v1/transactions?month=${MESES[0]}&limit=100`)
    ).json()) as {
      items: Array<{ kind: string; categoryId: string | null; transferGroupId: string | null }>
    }
    const pernas = dezembro.items.filter((i) => i.kind.startsWith('transfer_'))
    expect(pernas.map((p) => p.kind).sort()).toEqual(['transfer_in', 'transfer_out'])
    expect(pernas[0]?.transferGroupId).toBeTruthy()
    expect(pernas[1]?.transferGroupId).toBe(pernas[0]?.transferGroupId)
    expect(pernas.every((p) => p.categoryId === null)).toBe(true)
  })

  /** Reimportar o MESMO extrato depois do reprocessamento: a perna convertida
   *  continua caindo em `duplicado_exato` (aceite 41 pela tela) — a chave de
   *  deduplicação sobreviveu à conversão. */
  test('reimportar o extrato depois do reprocessamento: tudo duplicado_exato, nada entra', async ({
    page,
  }) => {
    await page.goto('/importar')
    await page.getByLabel('Conta de destino').selectOption({ label: CONTA_Q })
    await page.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
      name: 'quorbix-de-novo.csv',
      mimeType: 'text/csv',
      buffer: extratoCSV(EXTRATO_Q),
    })
    await page.getByRole('button', { name: 'Analisar arquivo' }).click()
    await expect(
      page.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
    ).toBeVisible()
    await expect(page.getByText(/0 prontas para importar/)).toBeVisible()
    await expect(page.getByText(/4 ficam de fora/)).toBeVisible()
    await expect(page.getByRole('rowheader', { name: /Parece transferência/ })).toHaveCount(0)
    await expect(page.getByRole('rowheader', { name: /já registrada/i })).toHaveCount(0)

    // Direto na API, a contagem por status: os quatro são duplicado_exato.
    const url = page.url()
    const loteID = url.match(/\/importar\/([0-9a-f-]+)\/revisar$/)?.[1]
    expect(loteID, 'o id do lote está na URL').toBeTruthy()
    const revisao = (await (await page.request.get(`/api/v1/imports/${loteID}?limit=200`)).json()) as {
      items: Array<{ status: string }>
      batch: {
        counts: { duplicado_exato: number; novo: number; transferencia_interna: number }
      }
    }
    expect(revisao.items.map((i) => i.status)).toEqual([
      'duplicado_exato',
      'duplicado_exato',
      'duplicado_exato',
      'duplicado_exato',
    ])
    expect(revisao.batch.counts.duplicado_exato).toBe(4)
    expect(revisao.batch.counts.novo).toBe(0)
    expect(revisao.batch.counts.transferencia_interna).toBe(0)
  })
})
