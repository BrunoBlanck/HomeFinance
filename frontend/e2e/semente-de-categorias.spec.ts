import { expect, type Locator, type Page, test } from '@playwright/test'
import { BASE_URL } from './support/ambiente'
import { entrarComContaNova } from './support/sessao'

/** A semente de categorias vista pelo navegador (ADR-033, spec 0003 §5),
 *  contra a API Go real.
 *
 *  Casa nova nasce com **15 grupos, 41 subcategorias e as palavras-chave de
 *  fábrica**. Este spec é a cobertura E2E desse comportamento, e ele existe
 *  porque a alternativa foi recusada: propôs-se um interruptor de ambiente
 *  (`SEED_CATEGORIES=off`) que desligasse a semente na suíte, e a decisão do
 *  plano (§15) foi **não**. Um E2E que roda contra uma casa sem semente prova
 *  coisas sobre um app que ninguém usa, e apagaria justamente a cobertura do
 *  maior risco da feature — uma palavra de fábrica categorizando em silêncio o
 *  que não devia. O interruptor teria destruído este arquivo; os outros specs é
 *  que se adaptaram.
 *
 *  O que só este nível prova, e que nenhum teste de tabela do backend alcança:
 *
 *  - **a árvore chega inteira à tela.** Os 56 nomes atravessam `GET
 *    /categories`, o JSON, o cache do TanStack Query e quatro listas aninhadas.
 *    Um teste de tabela em Go afirma a lista; só aqui se vê a lista desenhada;
 *  - **a sugestão de fábrica funciona no PRIMEIRO extrato**, que é o momento em
 *    que o app prova que serve. A linha `IFOOD *IFOOD` chega à revisão já
 *    apontando `Alimentação › Delivery`, sem ninguém ter cadastrado nada — e a
 *    linha de Pix, que é o vocabulário de rotina de todo extrato, **não** ganha
 *    nada (é o risco nº 1 da §8 do plano, visto pela tela);
 *  - **a consequência estrutural aparece na UI.** Com 14 dos 15 grupos tendo
 *    filhas, "grupo não recebe palavra-chave" (spec 0005 §12) e "grupo não é
 *    opção de lançamento" (§13) deixaram de ser caso raro: são o caminho comum
 *    de casa nova, e é assim que o diálogo e o `<select>` têm de se comportar
 *    no primeiro dia.
 *
 *  **Casa PRÓPRIA**, e não a compartilhada do projeto `setup`: o assunto aqui é
 *  o estado INICIAL da taxonomia, e a casa compartilhada é justamente aquela em
 *  que os vizinhos criam, arquivam e renomeiam categoria. Roda em série — os
 *  testes contam a mesma história em ordem. */

test.describe.configure({ mode: 'serial' })

const CONTA = 'Conta QA Semente'
const MES = '2026-04'

/** Um grupo PRÓPRIO, sem filhas: é ele que sustenta as duas metades da §12 que
 *  a semente sozinha não mostra — "grupo folha aceita palavra" e "a palavra da
 *  semente já tem dona". */
const GRUPO_PROPRIO = 'QA Horta'

/** Quantos GRUPOS a semente cria por natureza, e quantas CATEGORIAS (grupos
 *  mais folhas) cada lista da tela passa a ter. Os quatro números somam 15 e
 *  56 — `DefaultCategoryCount()` do backend.
 *
 *  A CONTAGEM DE PALAVRAS não está aqui de propósito: quem a trava é o teste de
 *  tabela do pacote `category` (§7 do plano), que também confere as regras
 *  R1–R8 e a lista de tokens perigosos. Repetir o número aqui daria dois lugares
 *  para atualizar e nenhuma garantia nova. O que este spec afirma sobre palavra
 *  é o que só a tela pode dizer: que ela CHEGA na folha certa e categoriza. */
const SEMENTE = [
  { natureza: 'Despesas', grupos: 11, categorias: 40 },
  { natureza: 'Receitas', grupos: 2, categorias: 8 },
  { natureza: 'Investimentos', grupos: 1, categorias: 4 },
  { natureza: 'Resgates', grupos: 1, categorias: 4 },
] as const

/** Os 15 grupos, na natureza em que a tela os desenha. */
const GRUPOS_POR_NATUREZA: Record<string, readonly string[]> = {
  Despesas: [
    'Moradia',
    'Alimentação',
    'Transporte',
    'Saúde',
    'Educação',
    'Lazer',
    'Compras',
    'Serviços',
    'Pessoal',
    'Impostos',
    'Outras despesas',
  ],
  Receitas: ['Salário', 'Outras receitas'],
  Investimentos: ['Investimentos'],
  Resgates: ['Resgates'],
}

type Linha = { dia: string; valor: string; id: string; descricao: string }

/** O primeiro extrato da casa. Cinco linhas que a semente reconhece ou recusa
 *  de propósito, e nenhuma palavra-chave cadastrada por ninguém.
 *
 *  As duas últimas são o contraponto, e valem tanto quanto as três primeiras:
 *
 *  - a linha de **Pix** é o vocabulário de rotina que aparece em todo extrato
 *    (nome de pessoa, banco, agência, conta). Se a semente a categorizasse,
 *    toda transferência da casa entraria num balde errado em silêncio — é a
 *    ameaça declarada na §8 do plano, e esta linha é a sentinela dela. O
 *    servidor sanitiza a descrição para `Pix enviado - JOAO DA SILVA` antes de
 *    guardá-la;
 *  - `ZUMBRA S` é um nome inventado, conferido contra o motor real: ele existe
 *    para sobrar sem categoria e virar a lacuna que os dois últimos testes
 *    usam. */
const EXTRATO: readonly Linha[] = [
  { dia: '03', valor: '-35.90', id: '01', descricao: 'Compra no débito - IFOOD *IFOOD' },
  {
    dia: '05',
    valor: '-182.44',
    id: '02',
    descricao: 'Pagamento de boleto efetuado - ENEL DISTRIBUICAO SAO PAULO',
  },
  { dia: '09', valor: '-21.30', id: '03', descricao: 'Uber *Trip' },
  {
    dia: '12',
    valor: '-500.00',
    id: '04',
    descricao:
      'Transferência enviada pelo Pix - JOAO DA SILVA - 123.456.789-00 - BANCO INTER S.A. (0077) Agência: 1 Conta: 123-4',
  },
  { dia: '15', valor: '-17.00', id: '05', descricao: 'ZUMBRA S' },
  { dia: '20', valor: '700.00', id: '06', descricao: 'Resgate RDB' },
  { dia: '22', valor: '35.90', id: '07', descricao: 'Estorno de IFOOD' },
]

/** A descrição do Pix DEPOIS do sanitizador do servidor: o documento corta o
 *  resto da linha e o prefixo verboso vira rótulo curto. É por este texto que a
 *  tela chama a linha. */
const PIX_NA_TELA = 'Pix enviado - JOAO DA SILVA'

function extratoCSV(linhas: readonly Linha[]): Buffer {
  const cabecalho = 'Data,Valor,Identificador,Descrição\n'
  const corpo = linhas
    .map(
      (l) =>
        `${l.dia}/04/2026,${l.valor},55555555-5555-4555-8555-5555555555${l.id},"${l.descricao}"\n`,
    )
    .join('')
  return Buffer.from(cabecalho + corpo, 'utf-8')
}

let page: Page

test.beforeAll(async ({ browser }) => {
  const contexto = await browser.newContext({
    baseURL: BASE_URL,
    locale: 'pt-BR',
    timezoneId: 'America/Sao_Paulo',
  })
  page = await contexto.newPage()
  await entrarComContaNova(page, 'QA Semente')
})

test.afterAll(async () => {
  // Higiene, não asserção — ver `atalho-de-categoria.spec.ts`.
  await page
    ?.context()
    .close()
    .catch(() => {})
})

// ------------------------------------------------------------- utilitários

type CategoriaJSON = {
  id: string
  name: string
  parentId: string | null
  keywords: string[]
  children: CategoriaJSON[]
}
type ArvoreJSON = Record<'expense' | 'income' | 'investment' | 'redemption', CategoriaJSON[]>

async function arvoreDaCasa(): Promise<ArvoreJSON> {
  const resposta = await page.request.get('/api/v1/categories')
  expect(resposta.status()).toBe(200)
  return (await resposta.json()) as ArvoreJSON
}

/** O `<select>` de categoria de uma linha da revisão da importação. */
function categoriaDe(descricao: string, data: string): Locator {
  return page.getByRole('combobox', { name: new RegExp(`^Categoria de ${descricao}, ${data},`) })
}

/** O rótulo do `<optgroup>` da opção escolhida, ou `''` quando ela está solta
 *  no `<select>` — é o que distingue "a folha X, do grupo Y" de "o grupo Y". */
async function grupoDaEscolhida(seletor: Locator): Promise<string> {
  return seletor
    .locator('option:checked')
    .evaluate((opcao) => (opcao.parentElement as HTMLOptGroupElement | null)?.label ?? '')
}

function campoDePalavras(escopo: Page | Locator): Locator {
  return escopo.getByLabel('Palavras-chave', { exact: true })
}

// ------------------------------------------------------------------ testes

test.describe('a casa nova nasce com a taxonomia', () => {
  test('/categorias mostra os 15 grupos, cada um com as suas subcategorias', async () => {
    await page.goto('/categorias')
    await expect(page.getByRole('heading', { level: 1, name: 'Categorias' })).toBeVisible()

    // Cada lista tem exatamente os grupos da semente e o total de linhas que a
    // árvore promete. A contagem é pelo botão "Editar X", que existe uma vez
    // por categoria — grupo e folha.
    for (const { natureza, grupos, categorias } of SEMENTE) {
      const regiao = page.getByRole('region', { name: natureza })
      await expect(regiao).toBeVisible()
      await expect(regiao.getByRole('button', { name: /^Editar / })).toHaveCount(categorias)
      // "Nova subcategoria em X" só existe no GRUPO: é a contagem de grupos.
      await expect(regiao.getByRole('button', { name: /^Nova subcategoria em / })).toHaveCount(
        grupos,
      )
      for (const grupo of GRUPOS_POR_NATUREZA[natureza] ?? []) {
        // Pelo botão da linha, e não por `getByText`: o `<h2>` do painel
        // "Investimentos" tem o mesmo texto do grupo "Investimentos", e o
        // `exact: true` do nome acessível é o que separa "Editar Salário" de
        // "Editar Salário, 13º e férias".
        await expect(
          regiao.getByRole('button', { name: `Editar ${grupo}`, exact: true }),
        ).toHaveCount(1)
      }
    }

    // As subcategorias estão DENTRO do grupo, e não soltas na lista: a `<ul>`
    // aninhada é o que diz "isto está dentro daquilo" para o leitor de tela.
    const despesas = page.getByRole('region', { name: 'Despesas' })
    const moradia = despesas.getByRole('listitem').filter({
      has: page.getByRole('button', { name: 'Editar Moradia' }),
    })
    const filhasDeMoradia = moradia.getByRole('list').getByRole('listitem')
    await expect(filhasDeMoradia).toHaveCount(3)
    // Conteúdo exato, ORDEM não: quem ordena é o `ORDER BY name` do banco, e a
    // ordenação de acento é da COLAÇÃO do dialeto — no SQLite "Água" vem depois
    // de "Manutenção", no PostgreSQL com locale pt_BR viria antes. Afirmar a
    // ordem aqui seria afirmar o SQLite, não o produto (docs/BANCO-DE-DADOS.md:
    // o schema é portátil entre os quatro).
    expect((await filhasDeMoradia.allTextContents()).map((t) => t.trim()).sort()).toEqual(
      ['Aluguel e condomínio', 'Água, luz e gás', 'Manutenção e reforma'].sort(),
    )

    // E o balde residual não tem filha nenhuma: é ele que continua recebendo
    // lançamento direto quando nada mais serve.
    const outras = despesas.getByRole('listitem').filter({
      has: page.getByRole('button', { name: 'Editar Outras despesas' }),
    })
    await expect(outras.getByRole('list')).toHaveCount(0)
  })

  test('a árvore que o servidor manda: 15 grupos, 41 subcategorias, e palavra só na folha', async () => {
    const arvore = await arvoreDaCasa()
    const grupos = [
      ...arvore.expense,
      ...arvore.income,
      ...arvore.investment,
      ...arvore.redemption,
    ]

    expect(grupos).toHaveLength(15)
    expect(grupos.reduce((total, g) => total + g.children.length, 0)).toBe(41)
    // Todo grupo é raiz e toda folha aponta para o pai: a árvore tem DOIS
    // níveis, nunca três.
    for (const grupo of grupos) {
      expect(grupo.parentId).toBeNull()
      for (const filha of grupo.children) {
        expect(filha.parentId).toBe(grupo.id)
        expect(filha.children).toHaveLength(0)
        // Folha semeada sem palavra seria uma folha que não categoriza nada.
        expect(filha.keywords.length).toBeGreaterThan(0)
      }
      // Grupo COM filhas nunca tem palavra — senão ela ficaria inerte, que é o
      // caso residual que a spec 0005 §12 existe para evitar.
      if (grupo.children.length > 0) expect(grupo.keywords).toEqual([])
    }

    // "Outras despesas" é o único grupo sem filhas, e nasce sem palavra também.
    const outras = arvore.expense.find((c) => c.name === 'Outras despesas')
    expect(outras).toBeTruthy()
    expect(outras?.children).toEqual([])
    expect(outras?.keywords).toEqual([])
    expect(arvore.expense.filter((c) => c.children.length === 0)).toHaveLength(1)
  })
})

test.describe('o primeiro extrato já vem sugerido', () => {
  test('a conta existe antes do resto', async () => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta', exact: true }).click()
    const dialogo = page.locator('dialog[open]')
    await dialogo.getByLabel('Nome').fill(CONTA)
    await dialogo.getByLabel('Tipo').selectOption('checking')
    await dialogo.getByRole('button', { name: 'Criar conta' }).click()
    await expect(page.getByRole('row', { name: new RegExp(CONTA) })).toBeVisible()
  })

  test('sem ninguém cadastrar nada, três linhas chegam com a folha certa — e o Pix com nenhuma', async () => {
    await page.goto('/importar')
    await page.getByLabel('Conta de destino').selectOption({ label: CONTA })
    await page.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
      name: 'NU_2026-04.csv',
      mimeType: 'text/csv',
      buffer: extratoCSV(EXTRATO),
    })
    await page.getByRole('button', { name: 'Analisar arquivo' }).click()
    await expect(
      page.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
    ).toBeVisible()

    // A sugestão é sempre a FOLHA, com o grupo no `<optgroup>`: os 14 grupos
    // com filhas não são opção (spec 0005 §13), nem para o servidor sugerir.
    for (const [descricao, data, folha, grupo] of [
      ['Compra no débito - IFOOD \\*IFOOD', '03/04', 'Delivery', 'Alimentação'],
      [
        'Pagamento de boleto efetuado - ENEL DISTRIBUICAO SAO PAULO',
        '05/04',
        'Água, luz e gás',
        'Moradia',
      ],
      ['Uber \\*Trip', '09/04', 'Aplicativo e transporte público', 'Transporte'],
      ['Resgate RDB', '20/04', 'Renda fixa e Tesouro Direto', 'Resgates'],
      ['Estorno de IFOOD', '22/04', 'Reembolsos e estornos', 'Outras receitas'],
    ] as const) {
      const seletor = categoriaDe(descricao, data)
      await expect(seletor.locator('option:checked')).toHaveText(folha)
      expect(await grupoDaEscolhida(seletor), `${descricao} devia vir do grupo ${grupo}`).toBe(grupo)
    }

    // O contraponto, que é o que torna as cinco de cima uma informação: o
    // vocabulário de rotina do extrato NÃO é categorizado. Uma palavra de
    // fábrica que casasse "Conta:", "BANCO INTER" ou um sobrenome comum poria
    // todas as transferências da casa num balde errado, em silêncio.
    await expect(categoriaDe(PIX_NA_TELA, '12/04').locator('option:checked')).toHaveText(
      'Sem categoria',
    )
    await expect(categoriaDe('ZUMBRA S', '15/04').locator('option:checked')).toHaveText(
      'Sem categoria',
    )

    await page.getByRole('button', { name: 'Importar 7 lançamentos' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Importação concluída' })).toBeVisible()

    // E a categoria chegou gravada: em /lancamentos a linha está na folha.
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(
      page
        .getByRole('row', { name: /IFOOD \*IFOOD/ })
        .getByRole('button', { name: /^Delivery\. Trocar categoria de / })
        .filter({ visible: true }),
    ).toHaveCount(1)
    // As duas que a semente não reconhece são a pendência do mês.
    await expect(
      page.getByRole('alert').filter({ hasText: /sem categoria/ }),
    ).toContainText('2 lançamentos de abril estão sem categoria')
  })
})

test.describe('grupo com filhas não recebe palavra-chave (spec 0005 §12)', () => {
  test('o diálogo do grupo esconde o campo; o da folha mostra, com a palavra de fábrica', async () => {
    await page.goto('/categorias')

    await page.getByRole('button', { name: 'Editar Alimentação' }).click()
    const doGrupo = page.locator('dialog[open]')
    await expect(doGrupo.getByRole('heading', { name: 'Editar categoria' })).toBeVisible()
    await expect(campoDePalavras(doGrupo)).toHaveCount(0)
    await expect(doGrupo.getByText('Palavras-chave ficam nas subcategorias.')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.locator('dialog[open]')).toHaveCount(0)

    // A folha tem o campo, e a palavra que categorizou o primeiro extrato está
    // lá — visível e removível, como qualquer palavra que a pessoa tivesse
    // digitado. Nenhuma categoria da semente é "de sistema".
    await page.getByRole('button', { name: 'Editar Delivery' }).click()
    const daFolha = page.locator('dialog[open]')
    await expect(campoDePalavras(daFolha)).toBeVisible()
    await expect(daFolha.getByText('Palavras-chave ficam nas subcategorias.')).toHaveCount(0)
    await expect(daFolha.getByRole('button', { name: 'Remover ifood', exact: true })).toHaveCount(1)
    await page.keyboard.press('Escape')
    await expect(page.locator('dialog[open]')).toHaveCount(0)
  })

  test('o servidor sustenta a regra sozinho: 400 no grupo, 409 na palavra que já tem dona', async () => {
    const arvore = await arvoreDaCasa()

    // (a) o grupo recusa a LISTA, mesmo que o corpo chegue por fora da tela.
    const alimentacao = arvore.expense.find((c) => c.name === 'Alimentação')
    expect(alimentacao).toBeTruthy()
    const recusa = await page.request.patch(`/api/v1/categories/${alimentacao?.id}`, {
      data: { keywords: ['zumbrete'] },
    })
    expect(recusa.status()).toBe(400)
    const erroDoGrupo = (await recusa.json()) as {
      error: { code: string; fields: Record<string, string> }
    }
    expect(erroDoGrupo.error.code).toBe('VALIDATION_FAILED')
    expect(erroDoGrupo.error.fields.keywords).toBe('Palavras-chave ficam nas subcategorias.')

    // (b) um grupo PRÓPRIO sem filhas aceita palavra — a regra é sobre TER
    // subcategoria, não sobre ser grupo.
    await page.goto('/categorias')
    await page.getByRole('button', { name: 'Novo grupo', exact: true }).click()
    const dialogo = page.locator('dialog[open]')
    await dialogo.getByLabel('Nome').fill(GRUPO_PROPRIO)
    await dialogo.getByLabel('Natureza').selectOption('expense')
    await dialogo.getByRole('button', { name: /^Criar/ }).click()
    await expect(dialogo).toHaveCount(0)

    const comOProprio = await arvoreDaCasa()
    const proprio = comOProprio.expense.find((c) => c.name === GRUPO_PROPRIO)
    expect(proprio).toBeTruthy()
    const aceita = await page.request.patch(`/api/v1/categories/${proprio?.id}`, {
      data: { keywords: ['zumbra'] },
    })
    expect(aceita.status()).toBe(200)

    // (c) e a palavra da SEMENTE já tem dona: pedi-la para outra categoria é
    // 409, com a folha de fábrica apontada como proprietária.
    const supermercado = comOProprio.expense
      .flatMap((g) => g.children)
      .find((c) => c.name === 'Supermercado')
    expect(supermercado).toBeTruthy()
    const colisao = await page.request.patch(`/api/v1/categories/${proprio?.id}`, {
      data: { keywords: ['zumbra', 'supermercado'] },
    })
    expect(colisao.status()).toBe(409)
    const erroDaColisao = (await colisao.json()) as {
      error: { code: string; fields: Record<string, string> }
    }
    expect(erroDaColisao.error.code).toBe('KEYWORD_TAKEN')
    expect(erroDaColisao.error.fields.ownerId).toBe(supermercado?.id)
  })
})

test.describe('grupo com filhas não recebe lançamento (spec 0005 §13)', () => {
  test('o <select> do atalho oferece as folhas em optgroup, e nunca o grupo', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await page
      .getByRole('button', { name: /^Sem categoria\. Categorizar ZUMBRA S,/ })
      .filter({ visible: true })
      .click()

    const editor = page.getByRole('group', { name: /^Categorizar ZUMBRA S,/ })
    await expect(editor).toBeVisible()
    const select = editor.getByRole('combobox')

    // "Alimentação" existe como CABEÇALHO do grupo de opções, nunca como opção.
    expect(await select.locator('optgroup[label="Alimentação"]').count()).toBe(1)
    await expect(select.locator('option').filter({ hasText: /^Alimentação$/ })).toHaveCount(0)
    // As folhas dela são as opções, e vivem dentro desse cabeçalho.
    await expect(
      select.locator('optgroup[label="Alimentação"] > option').filter({ hasText: /^Delivery$/ }),
    ).toHaveCount(1)

    // O único grupo SEM filhas continua sendo opção ele mesmo, solto: é o que
    // prova que a ausência dos outros 14 é a regra §13 agindo, e não uma lista
    // que simplesmente não traz grupo nenhum.
    const residual = select.locator('option').filter({ hasText: /^Outras despesas$/ })
    await expect(residual).toHaveCount(1)
    expect(await residual.evaluate((o) => o.parentElement?.tagName)).toBe('SELECT')

    // E o outro lado do dinheiro não vaza: nenhuma folha de receita aqui.
    expect(await select.locator('optgroup[label="Salário"]').count()).toBe(0)

    await page.keyboard.press('Escape')
    await expect(editor).toHaveCount(0)
  })

  test('a nota explica por que a categoria atual sumiu da lista quando o grupo ganha filha', async () => {
    // A linha vai para o grupo PRÓPRIO, que ainda é folha e por isso aceita
    // lançamento.
    await page.goto(`/lancamentos?mes=${MES}`)
    await page
      .getByRole('button', { name: /^Sem categoria\. Categorizar ZUMBRA S,/ })
      .filter({ visible: true })
      .click()
    const editor = page.getByRole('group', { name: /^Categorizar ZUMBRA S,/ })
    await editor.getByRole('combobox').selectOption({ label: GRUPO_PROPRIO })
    await editor.getByRole('button', { name: 'Categorizar', exact: true }).click()
    await expect(page.getByText(`Lançamento categorizado como ${GRUPO_PROPRIO}.`)).toBeVisible()

    // Agora ele ganha a primeira subcategoria — o mesmo estado em que os 14
    // grupos da semente NASCEM.
    await page.goto('/categorias')
    await page.getByRole('button', { name: `Nova subcategoria em ${GRUPO_PROPRIO}` }).click()
    await page.getByLabel('Nome').fill('Folha QA')
    await page.getByRole('button', { name: 'Criar' }).click()
    await expect(
      page.getByRole('region', { name: 'Despesas' }).getByText('Folha QA', { exact: true }),
    ).toBeVisible()

    // A marcação antiga sobrevive — mas o `<select>` não oferece mais o grupo,
    // e a tela DIZ por quê, nomeando o substantivo antes do nome (o nome
    // injetado nunca governa concordância: docs/DESIGN.md).
    await page.goto(`/lancamentos?mes=${MES}`)
    await page
      .getByRole('button', {
        name: new RegExp(`^${GRUPO_PROPRIO}\\. Trocar categoria de ZUMBRA S,`),
      })
      .filter({ visible: true })
      .click()
    const trocando = page.getByRole('group', { name: /^Trocar categoria de ZUMBRA S,/ })
    await expect(trocando).toBeVisible()
    await expect(trocando.getByRole('combobox')).toHaveValue('')
    await expect(
      trocando.getByText(
        `O grupo ${GRUPO_PROPRIO} tem subcategorias e não recebe lançamento. Escolha uma delas.`,
      ),
    ).toBeVisible()
    // E nunca a frase do outro motivo: são dois estados diferentes do mundo.
    await expect(trocando.getByText(/está arquivada/)).toHaveCount(0)

    await page.keyboard.press('Escape')
    await expect(trocando).toHaveCount(0)
  })
})
