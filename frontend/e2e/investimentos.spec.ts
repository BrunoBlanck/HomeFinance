import { expect, type Locator, type Page, test } from '@playwright/test'
import { BASE_URL } from './support/ambiente'
import { entrarComContaNova } from './support/sessao'

/** Ponta a ponta de investimentos e resgates (E7 / spec 0006, critério 13),
 *  contra a API Go real.
 *
 *  O que só este nível prova, e que nenhum teste de componente alcança:
 *
 *  - **o aporte some do relatório de despesas E continua no extrato.** Os dois
 *    números nascem de consultas diferentes — `SumByCategory` do relatório,
 *    `Summary` da lista, `SumInvestmentsByMonth` da tela de investimentos — e
 *    só um banco de verdade, com dado que veio de uma importação de verdade,
 *    diz se as três concordam. Com `fetch` mockado, cada tela devolve o que o
 *    autor escreveu;
 *  - **a subtração aparece nomeada na faixa do mês.** É a mitigação declarada
 *    do ADR-029(i) — sem ela, o total do mês encolhe sem explicação, que é
 *    mentira por omissão — e ela é a única parte da mitigação que o backend
 *    **não** garante: quem a cumpre é a tela;
 *  - **a sugestão da importação atravessa as naturezas novas** (spec 0006 §3.2
 *    e critério 5): a despesa "CDB 15 DIAS" chega à revisão já apontando a
 *    categoria de INVESTIMENTO, e a receita "RESGATE CDB" a de RESGATE. O lado
 *    do dinheiro é escolhido pelo servidor, e trocá-lo é justamente o defeito
 *    que um dublê esconderia;
 *  - **o número do ANO é outro número**, e não uma cópia do mês: só com dois
 *    meses importados no mesmo ano civil é que "no ano, até outubro" pode
 *    errar de forma visível.
 *
 *  **Casa PRÓPRIA**, e não a compartilhada do projeto `setup`. O motivo é
 *  aritmético: esta spec afirma o total do ANO, e um aporte que outra spec
 *  deixasse em qualquer mês de 2026 entraria nessa soma. Com o
 *  `RATE_LIMITS_PROFILE=test` do `global-setup.ts` o cadastro deixou de ser
 *  escasso, e a recomendação registrada em `sessao.setup.ts` é exatamente esta:
 *  quem precisa de isolamento ARITMÉTICO cadastra a sua. (Casa própria não
 *  isola mais de palavra-chave: desde a semente de categorias, ADR-033, toda
 *  casa nova nasce com a lista de fábrica inteira.)
 *
 *  **O que a semente mudou aqui** (18/09/2026): este spec cadastrava «cdb» em
 *  "Investimentos" e «resgate» em "Resgates" antes de importar. Não cadastra
 *  mais — os dois grupos ganharam filhas, e grupo com filha não aceita palavra
 *  nem é opção de `<select>` (spec 0005 §12/§13). As duas palavras vêm de
 *  fábrica, na folha "Renda fixa e Tesouro Direto" de cada árvore, e a sugestão
 *  da importação passou a apontar a FOLHA. A despesa comum do mês trocou de
 *  `MERCADO`/`PADARIA` para `DORNEK`/`MULFAZ`, nomes conferidos contra o motor
 *  real: ela precisa continuar sem categoria, e `MERCADO` casaria
 *  «supermercado» por aproximação.
 *
 *  Roda em série: os testes deste arquivo contam a mesma história em ordem. */

test.describe.configure({ mode: 'serial' })

const CONTA = 'Conta QA Investimentos'

/** O mês do assunto. Março entra junto, no MESMO ano civil: é ele que faz o
 *  número do ano diferir do número do mês — sem um segundo mês, "no ano"
 *  seria uma cópia de "no mês" e um erro na janela do ano passaria
 *  despercebido. */
const MES = '2026-10'

/** As folhas da semente que o mês inteiro exercita (ADR-033).
 *
 *  As duas se chamam igual — é proposital na semente, para a leitura das duas
 *  árvores ficar espelhada —, e é legal porque a unicidade de nome é entre
 *  IRMÃOS. Aqui elas nunca se confundem: o `<select>` de uma despesa só oferece
 *  categorias do lado que SAI da conta, e o de uma receita só as do lado que
 *  entra. O que distingue as duas na tela é o `<optgroup>`, e é ele que este
 *  spec confere. */
const FOLHA_DE_RENDA_FIXA = 'Renda fixa e Tesouro Direto'

type Linha = { dia: string; valor: string; id: string; descricao: string }

/** Março: um aporte só, que existe para o total do ANO. */
const EXTRATO_MARCO: readonly Linha[] = [
  { dia: '11', valor: '-1000.00', id: '01', descricao: 'CDB TESOURO QA' },
]

/** Outubro: o aporte do enunciado, um resgate e uma despesa comum.
 *
 *  A despesa comum não é enfeite: é ela que mantém o relatório de despesas
 *  NÃO-vazio depois da exclusão do aporte. Com o mês inteiro marcado, "o
 *  relatório não mostra o aporte" passaria por um relatório que não mostra
 *  nada. */
const EXTRATO_OUTUBRO: readonly Linha[] = [
  { dia: '05', valor: '-2000.00', id: '11', descricao: 'CDB 15 DIAS' },
  { dia: '12', valor: '850.00', id: '12', descricao: 'RESGATE CDB' },
  { dia: '20', valor: '-300.00', id: '13', descricao: 'DORNEK QA INVEST' },
]

/** Extrato Nubank: `Data,Valor,Identificador,Descrição`, `DD/MM/YYYY`,
 *  negativo é saída. Gerado aqui, e não lido de uma fixture do backend, pelo
 *  mesmo motivo de `importacao.spec.ts`: o dado que entra fica à vista de quem
 *  lê o teste. */
function extratoCSV(linhas: readonly Linha[], mes: string): Buffer {
  const cabecalho = 'Data,Valor,Identificador,Descrição\n'
  const corpo = linhas
    .map(
      (l) =>
        `${l.dia}/${mes}/2026,${l.valor},33333333-3333-4333-8333-3333333333${l.id},${l.descricao}\n`,
    )
    .join('')
  return Buffer.from(cabecalho + corpo, 'utf-8')
}

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
  await entrarComContaNova(page, 'QA Investimentos')
})

test.afterAll(async () => {
  // Fechar o contexto é higiene, não asserção — ver `atalho-de-categoria`.
  await page?.context().close().catch(() => {})
})

// ------------------------------------------------------------- utilitários

/** O primeiro `1.234,56` de um texto.
 *
 *  Lê o mesmo número em lugares que o formatam de jeitos diferentes (a faixa
 *  traz `R$` no rótulo falado, a coluna Valor não) sem acoplar o teste ao
 *  formato visível. */
function valorEm(texto: string | null | undefined): string {
  const achado = /[\d.]*\d,\d{2}/.exec(texto ?? '')
  if (!achado) throw new Error(`nenhum valor monetário em: ${JSON.stringify(texto)}`)
  return achado[0]
}

/** Uma linha da `<dl>` dos números: `<dt>` com a palavra e o período em
 *  `sr-only`, `<dd>` com o valor.
 *
 *  O período vem do sufixo `sr-only` (" em outubro" / " no ano, até outubro"),
 *  e é ele que distingue os QUATRO rótulos "Aportes"/"Resgates" da tela — em
 *  áudio e aqui, pelo mesmo motivo. */
function linhaDeNumero(escopo: Page, rotulo: string): Locator {
  return escopo.getByRole('term').filter({ hasText: rotulo })
}

async function numeroDe(escopo: Page, rotulo: string): Promise<string> {
  const termo = linhaDeNumero(escopo, rotulo)
  await expect(termo).toHaveCount(1)
  const valor = termo.locator('xpath=following-sibling::dd[1]')
  return valorEm(await valor.textContent())
}

async function contagemDe(escopo: Page, rotulo: string): Promise<string> {
  const termo = linhaDeNumero(escopo, rotulo)
  await expect(termo).toHaveCount(1)
  return (await termo.textContent())?.replace(rotulo, '').trim() ?? ''
}

/** O `<select>` de categoria de uma linha da revisão. */
function categoriaDe(pagina: Page, descricao: string, data: string): Locator {
  return pagina.getByRole('combobox', { name: new RegExp(`^Categoria de ${descricao}, ${data},`) })
}

/** O rótulo do `<optgroup>` da opção escolhida — o nome do GRUPO a que a folha
 *  pertence.
 *
 *  É o que distingue "Investimentos › Renda fixa e Tesouro Direto" de
 *  "Resgates › Renda fixa e Tesouro Direto", que se chamam igual na semente. */
async function grupoDaEscolhida(seletor: Locator): Promise<string> {
  return seletor
    .locator('option:checked')
    .evaluate((opcao) => (opcao.parentElement as HTMLOptGroupElement | null)?.label ?? '')
}

/** Envia um extrato e confirma a importação inteira. */
async function importar(
  pagina: Page,
  linhas: readonly Linha[],
  mes: string,
  conferirRevisao?: (pagina: Page) => Promise<void>,
): Promise<void> {
  await pagina.goto('/importar')
  await expect(
    pagina.getByRole('heading', { level: 1, name: 'Importar extrato ou fatura' }),
  ).toBeVisible()

  await pagina.getByLabel('Conta de destino').selectOption({ label: CONTA })
  await pagina.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
    name: `NU_2026-${mes}.csv`,
    mimeType: 'text/csv',
    buffer: extratoCSV(linhas, mes),
  })
  await pagina.getByRole('button', { name: 'Analisar arquivo' }).click()
  await expect(
    pagina.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
  ).toBeVisible()

  if (conferirRevisao) await conferirRevisao(pagina)

  await pagina.getByRole('button', { name: `Importar ${linhas.length} lançamento` }).click()
  await expect(
    pagina.getByRole('heading', { level: 1, name: 'Importação concluída' }),
  ).toBeVisible()
}

// ------------------------------------------------------------------ testes

test.describe('investimentos', () => {
  /** Critério 13, primeira frase: o item do menu leva à tela. */
  test('a casca leva a Investimentos, com a URL e o título certos', async () => {
    await page.goto('/')
    const item = page.getByRole('link', { name: 'Investimentos' })
    await expect(item).toBeVisible()
    await item.click()

    await expect(page).toHaveURL(/\/investimentos/)
    await expect(page.getByRole('heading', { level: 1, name: 'Investimentos' })).toBeVisible()
    await expect(page).toHaveTitle('Investimentos · HomeFinance')

    // A casa nasce com os grupos "Investimentos" e "Resgates" da semente
    // (ADR-029a), então a tela NÃO é o vazio de "nenhuma categoria ainda" —
    // ela é o mês sem movimento, com os números zerados e o convite a detectar.
    await expect(
      page.getByRole('heading', { name: 'Nenhuma categoria de investimento ainda.' }),
    ).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Detectar investimentos' }).first()).toBeVisible()
  })

  test('a conta existe, e a semente já reconhece o aporte e o resgate', async () => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta', exact: true }).click()
    const dialogo = page.locator('dialog[open]')
    await dialogo.getByLabel('Nome').fill(CONTA)
    await dialogo.getByLabel('Tipo').selectOption('checking')
    await dialogo.getByRole('button', { name: 'Criar conta' }).click()
    await expect(page.getByRole('row', { name: new RegExp(CONTA) })).toBeVisible()

    // Até 18/09/2026 este teste cadastrava «cdb» em "Investimentos" e
    // «resgate» em "Resgates". Os dois passos morreram com a semente (ADR-033):
    // os grupos ganharam filhas, e grupo com filha não aceita palavra-chave
    // (spec 0005 §12) nem aparece como opção no `<select>` (§13). O que eles
    // preparavam agora vem de fábrica, e é ISSO que a tela mostra aqui: as duas
    // folhas existem, com as palavras do enunciado do critério 13 já dentro.
    await page.goto('/categorias')
    for (const [grupo, palavra] of [
      ['Investimentos', 'cdb'],
      ['Resgates', 'resgate cdb'],
    ] as const) {
      const regiao = page.getByRole('region', { name: grupo })
      await expect(regiao.getByText(FOLHA_DE_RENDA_FIXA, { exact: true })).toBeVisible()
      await regiao.getByRole('button', { name: `Editar ${FOLHA_DE_RENDA_FIXA}` }).click()
      const dialogoDaFolha = page.locator('dialog[open]')
      // A ficha existe, e é a da semente: o × dela traz a palavra no rótulo.
      await expect(
        dialogoDaFolha.getByRole('button', { name: `Remover ${palavra}`, exact: true }),
      ).toHaveCount(1)
      await page.keyboard.press('Escape')
      await expect(page.locator('dialog[open]')).toHaveCount(0)
    }
  })

  /** Critério 5 no navegador: a sugestão da importação atravessa as naturezas
   *  novas, e escolhe o LADO do dinheiro. */
  test('a importação sugere a categoria de investimento — e nunca a do lado errado', async () => {
    await importar(page, EXTRATO_MARCO, '03')

    await importar(page, EXTRATO_OUTUBRO, '10', async (pagina) => {
      // Despesa → categoria de natureza `investment`. A folha é a da semente, e
      // o `<optgroup>` é quem diz de qual das duas árvores ela veio — as duas
      // folhas se chamam igual de propósito.
      const doAporte = categoriaDe(pagina, 'CDB 15 DIAS', '05/10')
      await expect(doAporte.locator('option:checked')).toHaveText(FOLHA_DE_RENDA_FIXA)
      expect(await grupoDaEscolhida(doAporte)).toBe('Investimentos')
      // Receita → categoria de natureza `redemption`. Nunca a de aporte: o
      // matcher é escolhido pelo lado do dinheiro (ADR-029g).
      const doResgate = categoriaDe(pagina, 'RESGATE CDB', '12/10')
      await expect(doResgate.locator('option:checked')).toHaveText(FOLHA_DE_RENDA_FIXA)
      expect(await grupoDaEscolhida(doResgate)).toBe('Resgates')
      // E a despesa comum, que não bate com nada, não ganha nada.
      await expect(
        categoriaDe(pagina, 'DORNEK QA INVEST', '20/10').locator('option:checked'),
      ).toHaveText('Sem categoria')
    })
  })

  /** Critério 13, segunda frase: a tela mostra o aporte NO MÊS e o número do
   *  ANO — que é outro número. */
  test('a tela mostra o aporte do mês, o do ano e a lista do mês', async () => {
    await page.goto(`/investimentos?mes=${MES}`)
    await expect(page.getByRole('heading', { level: 1, name: 'Investimentos' })).toBeVisible()

    // No mês: o aporte do enunciado e o resgate.
    expect(await numeroDe(page, 'Aportes em outubro')).toBe('2.000,00')
    expect(await contagemDe(page, 'Aportes em outubro')).toBe('1 lançamento')
    expect(await numeroDe(page, 'Resgates em outubro')).toBe('850,00')
    expect(await contagemDe(page, 'Resgates em outubro')).toBe('1 lançamento')

    // No ano: soma março + outubro. É aqui que a janela do ano se prova —
    // 3.000,00 é diferente de 2.000,00, e nenhuma cópia acerta os dois.
    expect(await numeroDe(page, 'Aportes no ano, até outubro')).toBe('3.000,00')
    expect(await contagemDe(page, 'Aportes no ano, até outubro')).toBe('2 lançamentos')
    expect(await numeroDe(page, 'Resgates no ano, até outubro')).toBe('850,00')

    // A tabela dos 12 meses é a FONTE DA VERDADE do gráfico (docs/DESIGN.md
    // E7 c): os dois meses com movimento aparecem nela, com os mesmos valores.
    const serie = page.getByRole('table', { name: /Aportes e resgates mês a mês/ })
    await expect(serie.getByRole('row', { name: /^outubro de 2026/ })).toContainText('2.000,00')
    await expect(serie.getByRole('row', { name: /^outubro de 2026/ })).toContainText('850,00')
    await expect(serie.getByRole('row', { name: /^março de 2026/ })).toContainText('1.000,00')
    // Mês sem movimento mostra `0,00`, nunca travessão: zero é um valor.
    await expect(serie.getByRole('row', { name: /^abril de 2026/ })).toContainText('0,00')

    // A lista do mês distingue aporte de resgate por PALAVRA, nunca só por cor.
    const itens = page.getByRole('table', { name: /Aportes e resgates de outubro de 2026/ })
    await expect(itens.getByRole('row', { name: /CDB 15 DIAS/ })).toContainText('Aporte')
    await expect(itens.getByRole('row', { name: /RESGATE CDB/ })).toContainText('Resgate')
    await expect(itens.getByRole('row', { name: /CDB 15 DIAS/ })).toContainText(CONTA)
    // A despesa comum do mês NÃO está aqui: a tela mostra o que foi marcado.
    await expect(itens.getByRole('row', { name: /DORNEK QA INVEST/ })).toHaveCount(0)
  })

  /** Critério 13, terceira frase — e o coração da entrega: o mesmo lançamento
   *  não aparece no relatório de despesas. */
  test('o aporte não aparece no relatório de despesas, e o total é só a despesa comum', async () => {
    await page.goto(`/relatorios/categorias?mes=${MES}`)
    await expect(page.getByRole('heading', { level: 1, name: 'Gastos por categoria' })).toBeVisible()

    // Nenhuma linha da categoria de investimento — nem no corpo, nem no total.
    await expect(page.getByRole('row', { name: /Investimentos/ })).toHaveCount(0)
    await expect(page.getByRole('row', { name: /CDB 15 DIAS/ })).toHaveCount(0)

    // O total do relatório é a despesa comum, e só ela. Se o aporte tivesse
    // ficado, seriam 2.300,00 — e a diferença é exatamente o aporte.
    //
    // A leitura é da ÚLTIMA célula da linha (a coluna `Valor`), e não do texto
    // da linha inteira: as colunas `Lançamentos` e `Participação` trazem "1" e
    // "100,00%", e um regex de dinheiro sobre a linha toda casaria "1100,00"
    // ali dentro. Foi o que aconteceu na primeira execução desta spec.
    const total = page
      .getByRole('row')
      .filter({ has: page.getByRole('rowheader', { name: 'Total' }) })
    await expect(total).toBeVisible()
    expect(valorEm(await total.getByRole('cell').last().textContent())).toBe('300,00')

    // E a faixa do topo conta a mesma história, com a contagem junto.
    await expect(page.getByText('em 1 lançamento')).toBeVisible()

    // O resgate também não entra no relatório de RECEITAS.
    await page.goto(`/relatorios/categorias?mes=${MES}&natureza=receitas`)
    await expect(page.getByRole('row', { name: /Resgates/ })).toHaveCount(0)
  })

  /** A mitigação que só o frontend completa (ADR-029 i): o lançamento continua
   *  no extrato E a subtração aparece NOMEADA na faixa do mês.
   *
   *  Sem esta linha, `Saiu` cairia de 2.300,00 para 300,00 sem nenhuma
   *  explicação na tela — o backend garante o campo, quem o exibe é a tela, e
   *  é por isso que a asserção mora aqui. */
  test('em /lancamentos o aporte continua visível e a faixa explica o que ficou fora', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(page.getByRole('heading', { level: 1, name: 'Lançamentos' })).toBeVisible()

    // O lançamento não sumiu do app: ele existe e saiu da conta.
    await expect(page.getByRole('row', { name: /CDB 15 DIAS/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /RESGATE CDB/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /DORNEK QA INVEST/ })).toBeVisible()

    // Entrou e Saiu deixaram de somar os marcados.
    expect(valorEm(await page.getByText(/^Saiu/).first().textContent())).toBe('300,00')
    expect(valorEm(await page.getByText(/^Entrou/).first().textContent())).toBe('0,00')

    // E a segunda linha da faixa diz, por escrito, o que ficou de fora.
    const fora = page.getByText('Fora destes números:')
    await expect(fora).toBeVisible()
    const linha = fora.locator('xpath=..')
    await expect(linha).toContainText('em aportes')
    await expect(linha).toContainText('em resgates')
    await expect(linha).toContainText('2.000,00')
    await expect(linha).toContainText('850,00')
  })

  /** O saldo da conta NÃO muda por causa da marcação: o dinheiro saiu mesmo
   *  (ADR-029e). É a quarta leitura sobre a mesma linha, e a única que responde
   *  "quanto eu tenho". */
  test('o saldo da conta continua descontado do aporte', async () => {
    await page.goto('/contas')
    const linha = page.getByRole('row', { name: new RegExp(CONTA) })
    await expect(linha).toBeVisible()
    // Abertura 0,00 − 1.000,00 (março) − 2.000,00 − 300,00 + 850,00 = −2.450,00.
    await expect(linha).toContainText('2.450,00')
  })
})

/** O ciclo `detect` inteiro no navegador: prévia → confirmação → o número do
 *  SERVIDOR na tela, e a segunda execução sem nada a fazer.
 *
 *  Este é o teste para o qual o balde `InvestmentDetect` foi afrouxado no
 *  perfil de teste (600/h, estouro 30, `support/global-setup.ts`): com os tetos
 *  de produção (60/h, estouro 3) uma única abertura de diálogo já gasta uma
 *  prévia, e encadear prévia + confirmação + prévia de novo esbarraria no
 *  limitador no meio do teste.
 *
 *  O mês é NOVEMBRO, próprio deste teste: os meses anteriores já foram
 *  categorizados pela importação, e `detect` só alcança lançamento SEM
 *  categoria. A linha entra sem categoria de propósito — a revisão desfaz a
 *  sugestão —, que é exatamente o estado de quem importou antes de cadastrar a
 *  palavra-chave. */
test.describe('detectar investimentos', () => {
  const MES_DO_DETECT = '2026-11'

  const EXTRATO_NOVEMBRO: readonly Linha[] = [
    { dia: '09', valor: '-400.00', id: '21', descricao: 'CDB TESOURO DIRETO' },
    { dia: '18', valor: '-77.00', id: '22', descricao: 'MULFAZ QA INVEST' },
  ]

  test('a linha entra SEM categoria, porque a revisão desfaz a sugestão', async () => {
    await importar(page, EXTRATO_NOVEMBRO, '11', async (pagina) => {
      const seletor = categoriaDe(pagina, 'CDB TESOURO DIRETO', '09/11')
      await expect(seletor.locator('option:checked')).toHaveText(FOLHA_DE_RENDA_FIXA)
      expect(await grupoDaEscolhida(seletor)).toBe('Investimentos')
      await seletor.selectOption({ label: 'Sem categoria' })
      await expect(seletor.locator('option:checked')).toHaveText('Sem categoria')
    })

    // A tela do mês fica vazia: há categoria de investimento na casa, mas
    // nenhum lançamento marcado.
    await page.goto(`/investimentos?mes=${MES_DO_DETECT}`)
    // O titulo do EmptyState e um paragrafo, nao um heading — conferido na
    // arvore de acessibilidade real do Chromium.
    await expect(page.getByText('Nenhum aporte ou resgate em novembro de 2026.')).toBeVisible()
  })

  test('a prévia diz o que vai acontecer e a confirmação grava o número do servidor', async () => {
    await page.goto(`/investimentos?mes=${MES_DO_DETECT}`)
    await page.getByRole('button', { name: 'Detectar investimentos' }).first().click()

    const dialogo = page.locator('dialog[open]')
    await expect(dialogo.getByRole('heading', { name: 'Detectar investimentos' })).toBeVisible()

    // A prévia é do SERVIDOR: um vira aporte, um continua sem categoria.
    await expect(dialogo.getByText('Viram aporte ou resgate · 1')).toBeVisible()
    await expect(dialogo.getByText('Continuam sem categoria · 1')).toBeVisible()
    await expect(dialogo.getByRole('row', { name: /CDB TESOURO DIRETO/ })).toBeVisible()

    // O rótulo do confirmar DIZ o que vai acontecer, com o número da prévia.
    const confirmar = dialogo.getByRole('button', { name: 'Marcar 1 lançamento' })
    await expect(confirmar).toBeVisible()
    await confirmar.click()

    // O toast traz o número que VOLTOU do servidor, não o da prévia.
    await expect(page.getByText('1 lançamento marcado como aporte ou resgate.')).toBeVisible()
    await expect(dialogo).toHaveCount(0)

    // E a tela se refez sozinha: a invalidação sob o prefixo `transactions`
    // alcança esta tela sem ninguém apertar nada.
    expect(await numeroDe(page, 'Aportes em novembro')).toBe('400,00')
    const itens = page.getByRole('table', { name: /Aportes e resgates de novembro de 2026/ })
    await expect(itens.getByRole('row', { name: /CDB TESOURO DIRETO/ })).toContainText('Aporte')
  })

  test('a segunda execução não tem nada a marcar', async () => {
    await page.goto(`/investimentos?mes=${MES_DO_DETECT}`)
    await page.getByRole('button', { name: 'Detectar investimentos' }).first().click()

    const dialogo = page.locator('dialog[open]')
    await expect(dialogo.getByText('Viram aporte ou resgate · 0')).toHaveCount(0)

    // Idempotência vista pela tela: o confirmar vira "Nada a marcar" e é
    // `aria-disabled`, nunca `disabled` (docs/DESIGN.md).
    const confirmar = dialogo.getByRole('button', { name: 'Nada a marcar' })
    await expect(confirmar).toBeVisible()
    await expect(confirmar).toHaveAttribute('aria-disabled', 'true')
    await expect(confirmar).not.toHaveAttribute('disabled', '')

    await dialogo.getByRole('button', { name: 'Cancelar' }).click()
    await expect(dialogo).toHaveCount(0)

    // Nada mudou: o número do mês continua o mesmo.
    expect(await numeroDe(page, 'Aportes em novembro')).toBe('400,00')
  })
})
