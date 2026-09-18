import { expect, type Locator, type Page, test } from '@playwright/test'
import { BASE_URL } from './support/ambiente'
import { entrarComContaNova } from './support/sessao'

/** Atalho de categorização em `/lancamentos` (emenda §11 da spec 0005,
 *  critério (e)) e a regra §12 no diálogo de categoria — contra a API Go real.
 *
 *  O cenário, ponta a ponta:
 *
 *  1. importar um extrato com `MERCADO X`, `MERCADO Y`, `MERCADO W` e
 *     `PADARIA Z`, sem nenhuma palavra-chave cadastrada → `/lancamentos`
 *     mostra quatro `Sem categoria`;
 *  2. **só este**: clicar na primeira lacuna, escolher Alimentação, confirmar
 *     → só esta linha muda, a faixa cai de 4 para 3 e o foco vai para a
 *     próxima lacuna;
 *  3. **com palavra-chave**: na segunda lacuna, escolher Alimentação e a ficha
 *     «mercado» → `PATCH /categories` + `PATCH /transactions/{id}` +
 *     `POST /transactions/auto-categorize` → o toast traz o número REAL do
 *     servidor ("mais 1 lançamento de maio categorizado") e o `MERCADO W`,
 *     que ninguém tocou, ganha a categoria sozinho;
 *  4. **409**: com «padaria» já cadastrada em Serviços, escolher Saúde e a
 *     ficha «padaria» na quarta linha → toast `«padaria» já está em Serviços.`
 *     e a linha continua sem categoria (nada mais é feito);
 *  5. **teclado**: Enter e Space abrem o editor, Escape fecha e devolve o foco;
 *  6. **375 px**: exatamente uma instância do botão visível e nenhuma rolagem
 *     horizontal;
 *  7. **§12**: o diálogo de um grupo COM subcategorias não mostra o campo de
 *     palavras-chave — e o servidor sustenta a mesma regra sozinho.
 *
 *  O que só este nível prova: a pontuação e o reprocessamento saem do
 *  `internal/textmatch` e do `auto-categorize` reais (o número do toast é o do
 *  servidor, nunca a contagem das linhas carregadas), o `<select>`, o
 *  `<dialog>` e o foco são os do Chromium — no jsdom o `checkVisibility()` que
 *  escolhe a instância do botão aprova as duas —, e a sequência de três
 *  escritas atravessa três rotas da API de verdade.
 *
 *  **Casa própria, e não a compartilhada do projeto `setup`.** O motivo que
 *  vale é de ISOLAMENTO: o cenário precisa de uma casa SEM palavra-chave
 *  nenhuma. Na casa compartilhada, «supermercado» (de Alimentação) casa
 *  `MERCADO X` por aproximação (88) e a linha entraria já categorizada,
 *  apagando exatamente o que este spec existe para testar.
 *
 *  Houve um segundo motivo, que **caducou em 17/09/2026**: `POST /imports` é
 *  limitado a 10/h por casa e a casa compartilhada gastava as dez entre
 *  `importacao`, `importacao-c6-recuperacao` e
 *  `palavras-chave-e-transferencias` — a décima primeira, que era a deste
 *  spec, respondia 429. Hoje a API de teste sobe com
 *  `RATE_LIMITS_PROFILE=test` (`support/global-setup.ts`; docs/SEGURANCA.md
 *  §5.2) e o teto não aperta mais. Em PRODUÇÃO ele continua valendo — o boot
 *  recusa o perfil frouxo lá.
 *
 *  O contexto é criado UMA vez para o arquivo inteiro, em `beforeAll`, e os
 *  testes (em série) compartilham a mesma página. */

test.describe.configure({ mode: 'serial' })

const CONTA = 'Conta QA Atalho'
const MES = '2026-05'

/** A página compartilhada pelos testes deste arquivo — com a sessão da casa
 *  própria, não a do `storageState` do projeto. */
let page: Page

test.beforeAll(async ({ browser }) => {
  // `browser.newContext` não herda o `use` do projeto: o que este spec precisa
  // (endereço base, idioma e fuso) vai explícito.
  const contexto = await browser.newContext({
    baseURL: BASE_URL,
    locale: 'pt-BR',
    timezoneId: 'America/Sao_Paulo',
  })
  page = await contexto.newPage()
  await entrarComContaNova(page, 'QA Atalho')
})

test.afterAll(async () => {
  // Fechar o contexto é higiene, não asserção: no Windows o Playwright às
  // vezes falha ao salvar o artefato de trace deste contexto criado à mão
  // (`ENOENT … .playwright-artifacts-N`), e deixar esse erro derrubar o último
  // teste do arquivo reprovaria uma feature que passou.
  await page?.context().close().catch(() => {})
})

type Linha = { dia: string; valor: string; id: string; descricao: string }

/** Quatro linhas de maio. A casa é nova e não tem palavra-chave nenhuma, então
 *  todas entram sem categoria — que é o estado em que o atalho existe. Três
 *  dizem «mercado» (a palavra que vai virar palavra-chave) e uma diz
 *  «padaria» (a do conflito). */
const EXTRATO: readonly Linha[] = [
  { dia: '03', valor: '-50.00', id: '01', descricao: 'MERCADO X' },
  { dia: '07', valor: '-33.00', id: '02', descricao: 'MERCADO Y' },
  { dia: '12', valor: '-22.00', id: '03', descricao: 'MERCADO W' },
  { dia: '20', valor: '-12.00', id: '04', descricao: 'PADARIA Z' },
]

function extratoCSV(linhas: readonly Linha[]): Buffer {
  const cabecalho = 'Data,Valor,Identificador,Descrição\n'
  const corpo = linhas
    .map(
      (l) =>
        `${l.dia}/05/2026,${l.valor},44444444-4444-4444-8444-4444444444${l.id},${l.descricao}\n`,
    )
    .join('')
  return Buffer.from(cabecalho + corpo, 'utf-8')
}

// ------------------------------------------------------------- utilitários

/** A instância VISÍVEL do botão da célula. São duas no DOM (a da coluna
 *  Categoria e a da linha secundária da descrição) e exatamente uma visível
 *  por faixa de largura — filtrar por visibilidade é o que o teste tem de
 *  fazer, e é o que o item 24 do checklist cobra. */
function lacuna(descricao: string): Locator {
  return page
    .getByRole('button', { name: new RegExp(`^Sem categoria\\. Categorizar ${descricao},`) })
    .filter({ visible: true })
}

/** As duas instâncias, por CSS: a escondida (`display: none`) não está na
 *  árvore de acessibilidade e `getByRole` não a enxergaria. */
function instanciasNoDom(descricao: string): Locator {
  return page.locator(`button[aria-label^="Sem categoria. Categorizar ${descricao},"]`)
}

/** O editor aberto na linha — `<fieldset>` com `<legend>` sr-only. */
function editorDe(descricao: string): Locator {
  return page.getByRole('group', { name: new RegExp(`^Categorizar ${descricao},`) })
}

/** A faixa "N lançamentos sem categoria" de `/lancamentos`. */
function faixaDePendencia(): Locator {
  return page.getByRole('alert').filter({ hasText: /sem categoria/ })
}

function linhaDa(descricao: string): Locator {
  return page.getByRole('row', { name: new RegExp(descricao) })
}

function campoDePalavras(escopo: Page | Locator): Locator {
  return escopo.getByLabel('Palavras-chave', { exact: true })
}

/** Cadastra uma palavra-chave numa categoria pelo diálogo — o mesmo caminho da
 *  pessoa, com o `<dialog>` nativo. */
async function cadastrarPalavra(categoria: string, palavra: string): Promise<void> {
  await page.goto('/categorias')
  await page.getByRole('button', { name: `Editar ${categoria}` }).click()
  const dialogo = page.locator('dialog[open]')
  await campoDePalavras(dialogo).fill(palavra)
  await campoDePalavras(dialogo).press('Enter')
  await dialogo.getByRole('button', { name: 'Salvar' }).click()
  await expect(page.getByText('Categoria atualizada.')).toBeVisible()
  await expect(dialogo).toHaveCount(0)
}

// ------------------------------------------------------------------ cenário

test.describe('atalho de categorização em /lancamentos', () => {
  test('a conta e o extrato existem, e as quatro linhas entram sem categoria', async () => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta', exact: true }).click()
    const dialogo = page.getByRole('dialog')
    await dialogo.getByLabel('Nome').fill(CONTA)
    await dialogo.getByLabel('Tipo').selectOption('checking')
    await dialogo.getByRole('button', { name: 'Criar conta' }).click()
    await expect(page.getByRole('row', { name: new RegExp(CONTA) })).toBeVisible()

    await page.goto('/importar')
    await page.getByLabel('Conta de destino').selectOption({ label: CONTA })
    await page.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
      name: 'NU_maio.csv',
      mimeType: 'text/csv',
      buffer: extratoCSV(EXTRATO),
    })
    await page.getByRole('button', { name: 'Analisar arquivo' }).click()
    await expect(
      page.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
    ).toBeVisible()

    // Sem palavra-chave na casa, nenhuma linha vem sugerida.
    await expect(
      page.getByRole('heading', { level: 2, name: 'Prontas para importar' }),
    ).toBeVisible()
    for (const linha of EXTRATO) {
      await expect(
        page
          .getByRole('combobox', { name: new RegExp(`^Categoria de ${linha.descricao},`) })
          .locator('option:checked'),
      ).toHaveText('Sem categoria')
    }

    await page.getByRole('button', { name: 'Importar 4 lançamentos' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Importação concluída' })).toBeVisible()

    await page.getByRole('button', { name: 'Ver os lançamentos de maio' }).click()
    await expect(page).toHaveURL(new RegExp(`/lancamentos\\?mes=${MES}`))
    await expect(faixaDePendencia()).toContainText('4 lançamentos de maio estão sem categoria')
    await expect(
      page.getByRole('button', { name: /^Sem categoria\. Categorizar / }).filter({ visible: true }),
    ).toHaveCount(4)
  })

  test('só este: a linha muda, a faixa cai de 4 para 3 e o foco vai à próxima lacuna', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(faixaDePendencia()).toContainText('4 lançamentos de maio estão sem categoria')

    await lacuna('MERCADO X').click()
    const editor = editorDe('MERCADO X')
    await expect(editor).toBeVisible()
    // O editor é uma linha da TABELA, não uma camada flutuante (item 17).
    expect(await editor.evaluate((no) => no.closest('table') !== null)).toBe(true)

    // O foco abre no `<select>`, que só oferece categorias de DESPESA.
    const select = editor.getByRole('combobox')
    await expect(select).toBeFocused()
    await expect(select.locator('option', { hasText: 'Salário' })).toHaveCount(0)

    // Sem categoria escolhida o confirmar explica em vez de desabilitar
    // (item 20: nenhum `disabled`).
    const semEscolha = editor.getByRole('button', { name: 'Escolha uma categoria' })
    await expect(semEscolha).toHaveAttribute('aria-disabled', 'true')
    expect(await semEscolha.evaluate((b) => (b as HTMLButtonElement).disabled)).toBe(false)

    await select.selectOption({ label: 'Alimentação' })
    const [patch] = await Promise.all([
      page.waitForRequest(
        (r) => /\/transactions\/[0-9a-f-]+$/.test(r.url()) && r.method() === 'PATCH',
      ),
      editor.getByRole('button', { name: 'Categorizar', exact: true }).click(),
    ])
    // O corpo tem UM campo — o resto do PATCH é da E2b.
    expect(Object.keys(patch.postDataJSON() as Record<string, unknown>)).toEqual(['categoryId'])

    await expect(page.getByText('Lançamento categorizado como Alimentação.')).toBeVisible()
    await expect(editor).toHaveCount(0)
    await expect(linhaDa('MERCADO X').getByText('Alimentação', { exact: true })).toBeVisible()
    await expect(faixaDePendencia()).toContainText('3 lançamentos de maio estão sem categoria')
    // A próxima lacuna é o próximo trabalho.
    await expect(lacuna('MERCADO Y')).toBeFocused()
    // E nenhuma outra linha foi tocada.
    await expect(lacuna('MERCADO W')).toHaveCount(1)
    await expect(lacuna('PADARIA Z')).toHaveCount(1)
  })

  test('com palavra-chave: «mercado» entra em Alimentação e o OUTRO do mês ganha categoria', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(faixaDePendencia()).toContainText('3 lançamentos de maio estão sem categoria')

    await lacuna('MERCADO Y').click()
    const editor = editorDe('MERCADO Y')
    await editor.getByRole('combobox').selectOption({ label: 'Alimentação' })

    // As fichas saem da descrição, e a pressionada se distingue sem cor: o
    // ícone troca e o rótulo do confirmar cita a palavra (itens 18 e 19).
    await expect(editor.getByText('Da próxima vez, reconhecer por')).toBeVisible()
    const ficha = editor.getByRole('button', { name: 'Reconhecer por «mercado»', exact: true })
    await expect(ficha).toHaveAttribute('aria-pressed', 'false')
    await ficha.click()
    await expect(ficha).toHaveAttribute('aria-pressed', 'true')
    await expect(editor.getByRole('status')).toHaveText(
      '«mercado» vira palavra-chave de Alimentação — vale para os outros lançamentos sem categoria de maio e para as próximas importações.',
    )

    // As três escritas da §11.3: (a) palavras, (b) esta linha, (c) o mês.
    const palavras = page.waitForRequest(
      (r) => /\/categories\/[0-9a-f-]+$/.test(r.url()) && r.method() === 'PATCH',
    )
    const lancamento = page.waitForRequest(
      (r) => /\/transactions\/[0-9a-f-]+$/.test(r.url()) && r.method() === 'PATCH',
    )
    const reprocessar = page.waitForRequest(
      (r) => r.url().endsWith('/transactions/auto-categorize') && r.method() === 'POST',
    )
    await editor.getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }).click()

    // (a) manda a lista inteira da categoria — aqui, a primeira palavra dela.
    expect((await palavras).postDataJSON()).toEqual({ keywords: ['mercado'] })
    expect(await lancamento).toBeTruthy()
    expect((await reprocessar).postDataJSON()).toEqual({ month: MES, dryRun: false })

    // O número do toast é o do SERVIDOR e conta só os OUTROS: MERCADO W. O
    // MERCADO X já estava categorizado e não é sobrescrito; o PADARIA Z não
    // casa com «mercado».
    await expect(
      page.getByText('«mercado» adicionada a Alimentação · mais 1 lançamento de maio categorizado.'),
    ).toBeVisible()
    await expect(editor).toHaveCount(0)

    await expect(linhaDa('MERCADO Y').getByText('Alimentação', { exact: true })).toBeVisible()
    await expect(linhaDa('MERCADO W').getByText('Alimentação', { exact: true })).toBeVisible()
    await expect(faixaDePendencia()).toContainText('1 lançamento de maio está sem categoria')
    // A que não diz «mercado» continua sendo a lacuna — e recebe o foco.
    await expect(lacuna('PADARIA Z')).toBeFocused()

    // A palavra ficou gravada na categoria (é o que a próxima importação usa).
    await page.goto('/categorias')
    await page.getByRole('button', { name: 'Editar Alimentação' }).click()
    await expect(
      page
        .locator('dialog[open]')
        .getByRole('list', { name: 'Palavras-chave adicionadas' })
        .getByRole('listitem'),
    ).toHaveText(['mercado'])
    await page.keyboard.press('Escape')
  })

  test('409: a palavra já é de outra categoria — o toast diz qual e nada mais é feito', async () => {
    // Serviços fica dona de «padaria» ANTES: é o conflito que a última linha
    // vai encontrar.
    await cadastrarPalavra('Serviços', 'padaria')

    await page.goto(`/lancamentos?mes=${MES}`)
    await lacuna('PADARIA Z').click()
    const editor = editorDe('PADARIA Z')
    await editor.getByRole('combobox').selectOption({ label: 'Saúde' })
    await editor.getByRole('button', { name: 'Reconhecer por «padaria»', exact: true }).click()

    const [resposta] = await Promise.all([
      page.waitForResponse(
        (r) => /\/categories\/[0-9a-f-]+$/.test(r.url()) && r.request().method() === 'PATCH',
      ),
      editor.getByRole('button', { name: 'Categorizar e reconhecer por «padaria»' }).click(),
    ])
    expect(resposta.status()).toBe(409)

    // A frase é nossa, com a dona pelo NOME — o `ownerId` nunca aparece.
    await expect(page.getByText('«padaria» já está em Serviços.')).toBeVisible()
    const corpo = (await resposta.json()) as { error: { fields: Record<string, string> } }
    await expect(page.getByText(corpo.error.fields.ownerId ?? 'id-que-nao-existe')).toHaveCount(0)

    // Nada mais foi feito: o editor continua aberto, a categoria escolhida
    // continua lá, a ficha saiu da linha, o rótulo voltou a `Categorizar` e o
    // foco está no confirmar.
    await expect(editor).toBeVisible()
    await expect(editor.getByRole('combobox').locator('option:checked')).toHaveText('Saúde')
    await expect(
      editor.getByRole('button', { name: 'Reconhecer por «padaria»', exact: true }),
    ).toHaveCount(0)
    await expect(editor.getByRole('button', { name: 'Categorizar', exact: true })).toBeFocused()
    // E o lançamento NÃO foi categorizado: a faixa não se mexeu.
    await expect(faixaDePendencia()).toContainText('1 lançamento de maio está sem categoria')

    // Fechar sem gravar mantém a lacuna.
    await page.keyboard.press('Escape')
    await expect(editor).toHaveCount(0)
    await expect(lacuna('PADARIA Z')).toBeFocused()
    await expect(linhaDa('PADARIA Z')).toContainText('Sem categoria')
  })

  test('teclado: Enter e Space abrem, Escape fecha e devolve o foco ao botão da célula', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)

    const botao = lacuna('PADARIA Z')
    await botao.focus()
    await expect(botao).toHaveAttribute('aria-expanded', 'false')
    await page.keyboard.press('Enter')

    const editor = editorDe('PADARIA Z')
    await expect(editor).toBeVisible()
    await expect(botao).toHaveAttribute('aria-expanded', 'true')
    await expect(botao).toHaveAttribute('aria-controls', (await editor.getAttribute('id')) ?? '')

    await page.keyboard.press('Escape')
    await expect(editor).toHaveCount(0)
    await expect(botao).toBeFocused()
    await expect(botao).toHaveAttribute('aria-expanded', 'false')

    await page.keyboard.press('Space')
    await expect(editorDe('PADARIA Z')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(botao).toBeFocused()
  })

  test('a 375 px: uma única instância do botão visível e nenhuma rolagem horizontal', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(faixaDePendencia()).toBeVisible()

    // Duas no DOM; em 1280 px a visível é a da COLUNA Categoria...
    await expect(instanciasNoDom('PADARIA Z')).toHaveCount(2)
    await expect(instanciasNoDom('PADARIA Z').filter({ visible: true })).toHaveCount(1)
    await expect(instanciasNoDom('PADARIA Z').filter({ visible: true })).toHaveAttribute(
      'id',
      /-coluna$/,
    )

    // ...e a 375 px, a da linha secundária da descrição.
    await page.setViewportSize({ width: 375, height: 800 })
    await expect(instanciasNoDom('PADARIA Z')).toHaveCount(2)
    const visivel = lacuna('PADARIA Z')
    await expect(visivel).toHaveCount(1)
    await expect(visivel).toHaveAttribute('id', /-secundaria$/)

    await visivel.click()
    const editor = editorDe('PADARIA Z')
    await expect(editor).toBeVisible()
    await editor.getByRole('combobox').selectOption({ label: 'Saúde' })

    // Com o editor aberto (o `colSpan` cobre colunas escondidas) a página
    // continua sem rolagem horizontal — item 24 do checklist.
    const rolagem = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      cliente: document.documentElement.clientWidth,
    }))
    expect(rolagem.scroll).toBeLessThanOrEqual(rolagem.cliente + 1)

    await page.keyboard.press('Escape')
    await expect(editor).toHaveCount(0)
    await page.setViewportSize({ width: 1280, height: 800 })
  })

  test('§12: o diálogo de um grupo com subcategorias não mostra o campo de palavras-chave', async () => {
    // O grupo ganha uma subcategoria agora: até aqui "Moradia" era folha e
    // aceitava palavra-chave.
    await page.goto('/categorias')
    await page.getByRole('button', { name: 'Nova subcategoria em Moradia' }).click()
    await page.getByLabel('Nome').fill('Energia QA')
    await page.getByRole('button', { name: 'Criar' }).click()
    await expect(
      page.getByRole('region', { name: 'Despesas' }).getByText('Energia QA', { exact: true }),
    ).toBeVisible()

    await page.getByRole('button', { name: 'Editar Moradia' }).click()
    const dialogo = page.locator('dialog[open]')
    await expect(dialogo.getByRole('heading', { name: 'Editar categoria' })).toBeVisible()
    await expect(campoDePalavras(dialogo)).toHaveCount(0)
    await expect(dialogo.getByText('Palavras-chave ficam nas subcategorias.')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.locator('dialog[open]')).toHaveCount(0)

    // A subcategoria tem o campo: é lá que a palavra vive.
    await page.getByRole('button', { name: 'Editar Energia QA' }).click()
    const daFilha = page.locator('dialog[open]')
    await expect(campoDePalavras(daFilha)).toBeVisible()
    await expect(daFilha.getByText('Palavras-chave ficam nas subcategorias.')).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(page.locator('dialog[open]')).toHaveCount(0)

    // E o servidor sustenta a regra sozinho: o grupo recusa a lista com 400 no
    // campo da LISTA, mesmo que o corpo chegue por fora da tela.
    const arvore = (await (await page.request.get('/api/v1/categories')).json()) as {
      expense: { id: string; name: string }[]
    }
    const moradia = arvore.expense.find((c) => c.name === 'Moradia')
    expect(moradia).toBeTruthy()
    const recusa = await page.request.patch(`/api/v1/categories/${moradia?.id}`, {
      data: { keywords: ['aluguel'] },
    })
    expect(recusa.status()).toBe(400)
    const erro = (await recusa.json()) as { error: { code: string; fields: Record<string, string> } }
    expect(erro.error.code).toBe('VALIDATION_FAILED')
    expect(erro.error.fields.keywords).toBe('Palavras-chave ficam nas subcategorias.')

    // O grupo sem filhas continua aceitando: a regra é sobre TER subcategoria.
    const saude = arvore.expense.find((c) => c.name === 'Saúde')
    const aceita = await page.request.patch(`/api/v1/categories/${saude?.id}`, {
      data: { keywords: ['dentista'] },
    })
    expect(aceita.status()).toBe(200)
  })
})
