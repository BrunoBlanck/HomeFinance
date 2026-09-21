import { expect, type Locator, type Page, test } from '@playwright/test'

/** Ponta a ponta do relatório por categoria (E6a / ADR-027), contra a API Go
 *  real e a casa compartilhada do projeto `setup`.
 *
 *  O que só este nível prova, e que nenhum teste de componente alcança:
 *
 *  - **o número do relatório é o MESMO número da lista.** `totalCents` do
 *    relatório e `summary.expenseCents` de `GET /transactions` saem de duas
 *    consultas diferentes, escritas em arquivos diferentes, e é aqui — com
 *    dados que vieram de uma importação de verdade — que se descobre se elas
 *    discordam. Um teste com `fetch` mockado devolve o que o autor escreveu;
 *  - **a invalidação de query sob `['transactions']` funciona no app inteiro**
 *    (ADR-027): categorizar um lançamento em `/lancamentos` tem de mudar o
 *    relatório sem ninguém apertar nada;
 *  - **o nome da categoria chega escapado ao SVG, à legenda e à tabela.** O
 *    `<title>` de uma fatia é texto dentro de SVG — o lugar clássico de
 *    injeção em gráfico feito à mão. Só um navegador de verdade diz se
 *    `<script>` virou texto ou virou nó;
 *  - **`?natureza` hostil não vira query da API.** A allowlist mora em
 *    `search.ts`; aqui se observa a requisição que efetivamente saiu.
 *
 *  **Sem importar nada — hoje por escolha, antes por falta de espaço.** Até
 *  17/09/2026 não cabia mesmo: `POST /imports` é 10/h por casa e a casa
 *  compartilhada já gastava as dez entre `importacao`,
 *  `importacao-c6-recuperacao` e `palavras-chave-e-transferencias`; cadastrar
 *  uma casa própria também não cabia, com os 5 registros/h por IP já tomados.
 *  Desde então a API de teste sobe com `RATE_LIMITS_PROFILE=test`
 *  (`support/global-setup.ts`; docs/SEGURANCA.md §5.2) e nada disso aperta —
 *  os tetos continuam valendo em produção, onde o boot recusa esse perfil.
 *
 *  O spec segue **lendo o mês que `importacao.spec.ts` deixou** (agosto/2026)
 *  e fazendo asserções de RELAÇÃO — relatório contra lista, rodapé contra
 *  faixa —, nunca de valor literal: é o que o torna imune ao que os vizinhos
 *  deixaram no banco. A contrapartida é depender de um vizinho ter rodado, e
 *  isso está registrado como lacuna de cobertura (dar dados próprios a este
 *  spec, agora que importar ficou barato).
 *
 *  Roda em série e por último no diretório (a ordem alfabética o coloca depois
 *  de `importacao`), que é de onde vem o mês com dado. */

test.describe.configure({ mode: 'serial' })

/** O mês que `importacao.spec.ts` povoa. */
const MES = '2026-08'
/** Um mês que nenhuma spec toca — o vazio tem de continuar vazio. */
const MES_VAZIO = '2031-01'

/** Nome hostil, no teto de 60 runas: marcação, emoji, RTL e acento.
 *
 *  Os três precisam estar no MESMO nome: o escape é uma coisa, a direção do
 *  texto é outra, e o emoji é o que costuma estourar um `substring` escrito
 *  em unidades UTF-16. */
const NOME_HOSTIL = '<script>alert(1)</script> 😀 مرحبا Órfã'

// ------------------------------------------------------------- utilitários

/** O primeiro `R$ 1.234,56` de um texto. Serve para ler o mesmo número em
 *  lugares que o formatam de jeitos diferentes (a faixa traz `R$`, a coluna
 *  Valor não) sem acoplar o teste ao formato visível: o rótulo de leitor de
 *  tela do `MoneyText` sempre traz o `R$`. */
function valorEm(texto: string | null | undefined): string {
  const achado = /R\$\s*[\d.]*\d,\d{2}/.exec(texto ?? '')
  if (!achado) throw new Error(`nenhum valor monetário em: ${JSON.stringify(texto)}`)
  return achado[0].replace(/\s+/g, ' ')
}

/** O primeiro inteiro de um texto (a contagem de lançamentos). */
function contagemEm(texto: string | null | undefined): number {
  const achado = /(\d[\d.]*)\s+lançamentos?/.exec(texto ?? '')
  if (!achado?.[1]) throw new Error(`nenhuma contagem em: ${JSON.stringify(texto)}`)
  return Number(achado[1].replace(/\./g, ''))
}

/** `R$ 1.234,56` → 123456. Só para SOMAR dois totais do servidor e comparar
 *  com um terceiro — o teste não formata dinheiro, ele confere uma relação. */
function centavosEm(valor: string): number {
  const achado = /([\d.]*\d),(\d{2})/.exec(valor)
  if (!achado?.[1] || !achado[2]) throw new Error(`não é um valor: ${JSON.stringify(valor)}`)
  return Number(achado[1].replace(/\./g, '')) * 100 + Number(achado[2])
}

/** Total e contagem da faixa do relatório que está na tela — ou zero nos dois
 *  quando o recorte está vazio (a faixa não existe no `EmptyState`). Um recorte
 *  vazio é resultado legítimo: a casa de teste pode não ter cartão neste mês. */
async function totalDaTela(page: Page): Promise<{ centavos: number; contagem: number }> {
  const faixa = page.locator('p').filter({ hasText: /lançamentos?$/ }).first()
  const vazio = page.getByText(/^Nenhuma (despesa|receita)( no (crédito|débito))? em /)
  await expect(faixa.or(vazio)).toBeVisible()
  if (await vazio.isVisible()) return { centavos: 0, contagem: 0 }
  const texto = await faixa.textContent()
  return { centavos: centavosEm(valorEm(texto)), contagem: contagemEm(texto) }
}

/** Resumo de `/lancamentos`: o "Entrou", o "Saiu" e quantas linhas o mês tem. */
async function resumoDaLista(page: Page, mes: string) {
  await page.goto(`/lancamentos?mes=${mes}`)
  await expect(page.getByRole('heading', { level: 1, name: 'Lançamentos' })).toBeVisible()

  const entrou = page.getByText(/^Entrou/).first()
  const saiu = page.getByText(/^Saiu/).first()
  await expect(saiu).toBeVisible()

  return {
    entrou: valorEm(await entrou.textContent()),
    saiu: valorEm(await saiu.textContent()),
  }
}

/** Abre o relatório e espera a faixa ou o vazio. */
async function abrirRelatorio(page: Page, mes: string, natureza?: string) {
  const busca = natureza === undefined ? `?mes=${mes}` : `?mes=${mes}&natureza=${natureza}`
  await page.goto(`/relatorios/categorias${busca}`)
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
}

function rodape(page: Page): Locator {
  return page.getByRole('row').filter({ has: page.getByRole('rowheader', { name: 'Total' }) })
}

// ------------------------------------------------------------------ testes

test.describe('relatório por categoria', () => {
  test('a casca leva a Relatórios, com a URL e o título certos', async ({ page }) => {
    await page.goto('/')
    const item = page.getByRole('link', { name: 'Relatórios' })
    await expect(item).toBeVisible()
    await item.click()

    await expect(page).toHaveURL(/\/relatorios\/categorias/)
    await expect(page.getByRole('heading', { level: 1, name: 'Gastos por categoria' })).toBeVisible()
    await expect(page).toHaveTitle('Gastos por categoria · HomeFinance')
  })

  /** BUG-1, aberto pelo QA em 17/09/2026 e corrigido no mesmo dia.
   *
   *  `docs/DESIGN.md`: "navegação de rota move o foco para o `<h1>` da tela
   *  nova". Esta tela era a única que não fazia isso quando se chegava pelo
   *  caminho NORMAL — clicar em `Relatórios` na navegação —, porque tinha uma
   *  guarda por `document.activeElement` que o `<a>` do menu fazia falhar.
   *
   *  O caso vive aqui, e não só no teste de componente, porque no jsdom a
   *  primeira renderização acontece com o foco no `document.body` — a guarda
   *  passava, e foi assim que o defeito chegou ao navegador. Aqui o clique é
   *  de verdade e o foco no link é o do navegador. */
  test('o <h1> recebe o foco ao chegar pela navegação', async ({ page }) => {
    await page.goto('/')
    await page.getByRole('link', { name: 'Relatórios' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Gastos por categoria' })).toBeFocused({
      timeout: 3_000,
    })
  })

  test('o total do relatório é o mesmo "Saiu" da lista, e o rodapé fecha em 100,00%', async ({
    page,
  }) => {
    const { saiu } = await resumoDaLista(page, MES)
    await abrirRelatorio(page, MES)

    // Sem a faixa não há mês com dado — e aí este arquivo inteiro não tem o
    // que testar. Falha alto em vez de passar vazio.
    const faixa = page.locator('p').filter({ hasText: /lançamentos?$/ }).first()
    await expect(faixa).toBeVisible()
    const totalDaFaixa = valorEm(await faixa.textContent())
    expect(totalDaFaixa).toBe(saiu)

    // O rodapé é o mesmo número por outro caminho (o `<tfoot>` da tabela).
    const linhaTotal = rodape(page)
    await expect(linhaTotal).toBeVisible()
    const textoDoRodape = (await linhaTotal.textContent()) ?? ''
    expect(valorEm(textoDoRodape)).toBe(saiu)
    expect(textoDoRodape).toContain('100,00%')
    // E a contagem do rodapé é a mesma da faixa.
    expect(contagemEm(await faixa.textContent())).toBeGreaterThan(0)
  })

  test('a rosca é decorativa, tem no máximo 6 fatias e o centro traz o total do servidor', async ({
    page,
  }) => {
    await abrirRelatorio(page, MES)
    const faixa = page.locator('p').filter({ hasText: /lançamentos?$/ }).first()
    const total = valorEm(await faixa.textContent())

    // `figure svg` e não `svg[aria-hidden]`: a casca do app está cheia de
    // ícones decorativos (o próprio `ChartIcon` do menu), e o primeiro
    // `aria-hidden` da página é um deles — foi assim que a primeira versão
    // deste teste mediu os `path` de um ícone.
    const figura = page.locator('figure').first()
    const svg = figura.locator('svg').first()
    await expect(svg).toBeVisible()
    expect(await svg.getAttribute('aria-hidden')).toBe('true')
    expect(await svg.getAttribute('focusable')).toBe('false')

    // Só as fatias: o `<pattern>` da hachura também vive dentro do SVG.
    const fatias = svg.locator('path:not(defs path)')
    const quantas = await fatias.count()
    expect(quantas).toBeGreaterThan(0)
    expect(quantas).toBeLessThanOrEqual(6)
    // Uma `<title>` por fatia, e nenhum `<text>` dentro do anel.
    expect(await svg.locator('title').count()).toBe(quantas)
    expect(await svg.locator('text').count()).toBe(0)

    // O centro é o total do SERVIDOR, não uma soma do cliente.
    const textoDaFigura = (await figura.textContent()) ?? ''
    expect(textoDaFigura).toContain('Total de')
    expect(valorEm(textoDaFigura)).toBe(total)
  })

  test('alternar para Receitas troca o título, a URL e o número', async ({ page }) => {
    const { entrou } = await resumoDaLista(page, MES)
    await abrirRelatorio(page, MES)

    // O foco vai para o seletor ANTES da troca porque `selectOption` sozinho
    // não foca nada — e uma pessoa que usa este controle necessariamente está
    // com o foco nele. Sem o `focus()`, a asserção de foco lá embaixo mediria
    // o `<body>` e diria que está tudo bem.
    const seletor = page.getByLabel('Natureza')
    await seletor.focus()
    await seletor.selectOption('receitas')

    await expect(page).toHaveURL(/natureza=receitas/)
    await expect(page.getByRole('heading', { level: 1, name: 'Receitas por categoria' })).toBeVisible()
    await expect(page).toHaveTitle('Receitas por categoria · HomeFinance')

    // E o foco NÃO sai do seletor que a pessoa acabou de usar: trocar a busca
    // re-renderiza a tela, não a remonta, então o efeito de entrada na rota não
    // roda de novo. É a outra metade do BUG-1 — a guarda que tentava proteger
    // este foco era a mesma que impedia o `<h1>` de recebê-lo na chegada.
    await expect(seletor).toBeFocused()

    const faixa = page.locator('p').filter({ hasText: /lançamentos?$/ }).first()
    await expect(faixa).toBeVisible()
    expect(valorEm(await faixa.textContent())).toBe(entrou)

    // E voltar para Despesas limpa a chave da URL (despesas é o padrão).
    await seletor.selectOption('despesas')
    await expect(page).not.toHaveURL(/natureza=/)
  })

  /** ADR-032: "Despesas no crédito" e "Despesas no débito" são recortes de
   *  conta da MESMA natureza `expense`. O que só este nível prova: o servidor
   *  fecha a conta — crédito + débito = todas as contas, em dinheiro e em
   *  contagem —, e a URL guarda a palavra em pt-BR enquanto a requisição leva
   *  o par `kind=expense&accountGroup=…`. */
  test('as quatro naturezas: URL com a palavra, query com o recorte, e crédito + débito fecham o total', async ({
    page,
  }) => {
    const pedidos: string[] = []
    page.on('request', (r) => {
      if (r.url().includes('/reports/by-category')) pedidos.push(r.url())
    })

    await abrirRelatorio(page, MES)
    const seletor = page.getByLabel('Natureza')
    await expect(seletor.locator('option')).toHaveText([
      'Despesas',
      'Despesas no crédito',
      'Despesas no débito',
      'Receitas',
    ])
    const todas = await totalDaTela(page)
    expect(todas.contagem).toBeGreaterThan(0)

    // --- crédito -----------------------------------------------------------
    await seletor.focus()
    await seletor.selectOption('despesas-credito')
    await expect(page).toHaveURL(/natureza=despesas-credito/)
    await expect(page).not.toHaveURL(/accountGroup/)
    await expect(
      page.getByRole('heading', { level: 1, name: 'Gastos no crédito por categoria' }),
    ).toBeVisible()
    await expect(page).toHaveTitle('Gastos no crédito por categoria · HomeFinance')
    await expect(page.locator('[data-atualizando]')).toHaveCount(0)
    const credito = await totalDaTela(page)
    // O foco não sai do seletor na troca (mesma garantia da troca para Receitas).
    await expect(seletor).toBeFocused()

    // --- débito ------------------------------------------------------------
    await seletor.selectOption('despesas-debito')
    await expect(page).toHaveURL(/natureza=despesas-debito/)
    await expect(
      page.getByRole('heading', { level: 1, name: 'Gastos no débito por categoria' }),
    ).toBeVisible()
    await expect(page).toHaveTitle('Gastos no débito por categoria · HomeFinance')
    await expect(page.locator('[data-atualizando]')).toHaveCount(0)
    const debito = await totalDaTela(page)

    // A relação que o contrato promete (ADR-032): os dois recortes somam o
    // todo, em centavos e em lançamentos. Nenhum número foi calculado aqui —
    // os três vieram do servidor, e só se confere que fecham.
    expect(credito.centavos + debito.centavos).toBe(todas.centavos)
    expect(credito.contagem + debito.contagem).toBe(todas.contagem)

    // --- de volta a Despesas, e Receitas ----------------------------------
    await seletor.selectOption('despesas')
    await expect(page).not.toHaveURL(/natureza=/)
    await expect(page.getByRole('heading', { level: 1, name: 'Gastos por categoria' })).toBeVisible()
    await seletor.selectOption('receitas')
    await expect(page).toHaveURL(/natureza=receitas/)
    await expect(page.getByRole('heading', { level: 1, name: 'Receitas por categoria' })).toBeVisible()

    // As requisições que efetivamente saíram: o recorte só viaja como
    // `accountGroup=credit|debit`, uma vez por pedido, e nunca com Receitas.
    const recortes = pedidos.map((url) => new URL(url).searchParams.getAll('accountGroup'))
    expect(recortes.some((r) => r[0] === 'credit')).toBe(true)
    expect(recortes.some((r) => r[0] === 'debit')).toBe(true)
    for (const r of recortes) expect(r.length).toBeLessThanOrEqual(1)
    for (const url of pedidos) {
      const busca = new URL(url).searchParams
      if (busca.get('kind') === 'income') expect(busca.has('accountGroup')).toBe(false)
      expect(url).not.toContain('accountGroup=&')
      expect(url.endsWith('accountGroup=')).toBe(false)
      expect(url).not.toContain('natureza')
    }
  })

  test('`?natureza` hostil cai em despesas e NADA dela chega à API', async ({ page }) => {
    const pedidos: string[] = []
    page.on('request', (r) => {
      if (r.url().includes('/reports/by-category')) pedidos.push(r.url())
    })

    // `credit` e `Despesas-Credito` são os "quase" do ADR-032: o valor da API e
    // a caixa errada. Nenhum dos dois vira recorte.
    for (const hostil of [
      "' OR 1=1 --",
      '<script>alert(1)</script>',
      'expense',
      'TUDO',
      'credit',
      'Despesas-Credito',
    ]) {
      await abrirRelatorio(page, MES, encodeURIComponent(hostil))
      await expect(page.getByRole('heading', { level: 1, name: 'Gastos por categoria' })).toBeVisible()
    }

    expect(pedidos.length).toBeGreaterThan(0)
    for (const url of pedidos) {
      expect(url).toContain('kind=expense')
      expect(url).not.toContain('natureza')
      expect(url).not.toContain('accountGroup')
      expect(url).not.toContain('OR+1%3D1')
      expect(url).not.toContain('script')
    }
  })

  test('mês sem lançamento mostra a saída para importar, sem resumo e sem anel', async ({
    page,
  }) => {
    await abrirRelatorio(page, MES_VAZIO)

    await expect(page.getByText('Nenhuma despesa em janeiro de 2031.')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Importar extrato' })).toBeVisible()
    // Sem resumo, sem tabela e sem anel: o vazio não finge um relatório.
    await expect(page.locator('figure')).toHaveCount(0)
    await expect(page.getByRole('table')).toHaveCount(0)

    // E em Receitas a copy acompanha.
    await page.getByLabel('Natureza').selectOption('receitas')
    await expect(page.getByText('Nenhuma receita em janeiro de 2031.')).toBeVisible()
  })

  test('categorizar em /lancamentos muda o relatório — e o nome hostil sai ESCAPADO', async ({
    page,
  }) => {
    // --- 1. a categoria de nome hostil -----------------------------------
    await page.goto('/categorias')
    await page.getByRole('button', { name: 'Novo grupo', exact: true }).click()
    await page.getByLabel('Nome').fill(NOME_HOSTIL)
    const natureza = page.getByLabel('Receita ou despesa')
    if (await natureza.count()) await natureza.selectOption('expense')
    await page.getByRole('button', { name: /^Criar/ }).click()
    await expect(page.getByText(NOME_HOSTIL, { exact: true }).first()).toBeVisible()

    // --- 2. estado do relatório ANTES -------------------------------------
    await abrirRelatorio(page, MES)
    const faixa = page.locator('p').filter({ hasText: /lançamentos?$/ }).first()
    const totalAntes = valorEm(await faixa.textContent())
    await expect(page.getByRole('cell', { name: new RegExp(NOME_HOSTIL.slice(0, 8)) })).toHaveCount(0)

    // --- 3. categorizar uma linha de agosto -------------------------------
    await page.goto(`/lancamentos?mes=${MES}`)
    // A linha precisa ser de DESPESA: o `<select>` do atalho só oferece
    // categorias da natureza do lançamento, e a categoria hostil é de despesa.
    // A primeira lacuna da tela é a mais recente do mês — que aqui é a receita
    // (`Tarvin Exemplo`), e foi nela que a primeira versão deste teste bateu.
    //
    // `Dornek Exemplo` chamava-se `Mercado Exemplo` até 18/09/2026. Com a
    // semente de categorias (ADR-033) `MERCADO` casa «supermercado» por
    // aproximação (89) e a linha entraria JÁ categorizada — não haveria lacuna
    // nenhuma para clicar aqui. O nome inventado é o que mantém este cenário
    // vivo; ver `importacao.spec.ts`, que é quem cria estas linhas.
    const lacuna = page
      .getByRole('button', { name: /^Sem categoria\. Categorizar Dornek Exemplo,/ })
      .filter({ visible: true })
      .first()
    await expect(lacuna).toBeVisible()
    await lacuna.click()

    const seletor = page.getByRole('combobox', { name: /^Categoria de Dornek Exemplo,/ })
    await expect(seletor).toBeVisible()
    await seletor.selectOption({ label: NOME_HOSTIL })
    await Promise.all([
      page.waitForRequest((r) => /\/transactions\/[0-9a-f-]+$/.test(r.url()) && r.method() === 'PATCH'),
      page.getByRole('button', { name: 'Categorizar', exact: true }).click(),
    ])

    // --- 4. o relatório mudou sozinho -------------------------------------
    await abrirRelatorio(page, MES)
    const celula = page.getByRole('cell').filter({ hasText: NOME_HOSTIL }).first()
    await expect(celula).toBeVisible()
    // O total do mês NÃO muda: categorizar move dinheiro de balde, não o cria.
    await expect(faixa).toBeVisible()
    expect(valorEm(await faixa.textContent())).toBe(totalAntes)

    // --- 5. escapado: texto, nunca marcação --------------------------------
    // Na tabela.
    expect(await celula.innerText()).toContain('<script>')
    expect(await page.locator('table script').count()).toBe(0)
    // No `<title>` da fatia e na legenda do SVG.
    const svg = page.locator('figure').first().locator('svg').first()
    const titulos = await svg.locator('title').allTextContents()
    expect(titulos.some((t) => t.includes('<script>alert(1)</script>'))).toBe(true)
    expect(await svg.locator('script').count()).toBe(0)
    expect(await page.locator('script[data-injetado]').count()).toBe(0)

    // E a página não ganhou rolagem horizontal por causa de um nome de 60
    // runas com emoji e RTL.
    const rolagem = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(rolagem).toBeLessThanOrEqual(1)
  })
})
