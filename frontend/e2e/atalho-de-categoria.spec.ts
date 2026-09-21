import { expect, type Locator, type Page, test } from '@playwright/test'
import { BASE_URL } from './support/ambiente'
import { entrarComContaNova } from './support/sessao'

/** Atalho de categorização em `/lancamentos` (emenda §11 da spec 0005,
 *  critério (e)) e a regra §12 no diálogo de categoria — contra a API Go real.
 *
 *  O cenário, ponta a ponta:
 *
 *  1. importar um extrato com `ZUMBRA X`, `ZUMBRA Y`, `ZUMBRA W` e
 *     `KREVOL Z`, quatro descrições que NADA na casa reconhece →
 *     `/lancamentos` mostra quatro `Sem categoria`;
 *  2. **só este**: clicar na primeira lacuna, escolher QA Feira, confirmar
 *     → só esta linha muda, a faixa cai de 4 para 3 e o foco vai para a
 *     próxima lacuna;
 *  3. **com palavra-chave**: na segunda lacuna, escolher QA Feira e a ficha
 *     «zumbra» → `PATCH /categories` + `PATCH /transactions/{id}` +
 *     `POST /transactions/auto-categorize` → o toast traz o número REAL do
 *     servidor ("mais 1 lançamento de maio categorizado") e o `ZUMBRA W`,
 *     que ninguém tocou, ganha a categoria sozinho;
 *  4. **409**: com «krevol» já cadastrada em QA Servico, escolher QA Cuidado e
 *     a ficha «krevol» na quarta linha → toast `«krevol» já está em QA
 *     Servico.` e a linha continua sem categoria (nada mais é feito);
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
 *  vale é de ISOLAMENTO — mas não mais o de "casa sem palavra-chave nenhuma".
 *  Desde a semente de categorias (ADR-033, 18/09/2026) essa casa não existe:
 *  TODA casa nova nasce com 15 grupos, 41 subcategorias e as palavras-chave de
 *  fábrica, e cadastrar uma conta nova não isola nada nesse eixo. O que a casa
 *  própria garante hoje é que não há palavra-chave **da pessoa** nem lançamento
 *  de vizinho: a faixa de pendência conta 4, e conta 4 porque este arquivo é o
 *  único que escreveu em maio.
 *
 *  O que substituiu o isolamento antigo são as **fixtures inventadas**. O que
 *  este spec prova é "linha sem sugestão vira lacuna → o atalho categoriza → a
 *  palavra-chave reprocessa o mês", e nada disso depende de a loja se chamar
 *  `ZUMBRA X` — que, aliás, a semente reconheceria por aproximação
 *  («supermercado», 89), fazendo a linha entrar já categorizada e apagando o
 *  cenário inteiro. `ZUMBRA` e `KREVOL` foram conferidos contra o motor real
 *  (`internal/textmatch` com a lista de `internal/category/seed.go`): nenhum
 *  alcança o limiar de 80 em nenhum dos dois lados do dinheiro.
 *
 *  **E os grupos da semente não servem de destino.** Os 14 que têm filhas
 *  viraram `<optgroup>` no `<select>` (só as folhas são opção, spec 0005 §13) e
 *  escondem o campo de palavras-chave no diálogo (§12). Por isso o spec cria
 *  **grupos próprios sem filhas** — `QA Feira`, `QA Servico`, `QA Cuidado` e
 *  `QA Abrigo` — e usa eles onde usava Alimentação, Serviços, Saúde e Moradia.
 *  O caso 7 depende disso duas vezes: ele precisa de um grupo que seja folha
 *  ANTES e ganhe a primeira filha DURANTE o teste, e "Moradia" já nasce com
 *  três.
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
 *  testes (em série) compartilham a mesma página.
 *
 *  ## ⚠️ Três casos da §19 que ainda NÃO existem — leia antes de escrevê-los
 *
 *  A emenda §19 da spec 0005 (recategorização manual em `/lancamentos`) previu
 *  três casos E2E que caberiam neste arquivo, sobre o cenário que ele já monta:
 *
 *  1. **recategorizar depois do "só este"** — trocar a categoria de uma linha
 *     que o passo 2 já categorizou, pelo controle fechado
 *     (`{categoria}. Trocar categoria de {descrição}`);
 *  2. **409 da palavra da categoria anterior** — pedir a ficha «x» para a
 *     categoria nova quando «x» já é da anterior;
 *  3. **viewport de 375 px** para o fluxo de troca (o caso 6 daqui cobre só a
 *     lacuna, não a troca).
 *
 *  Eles **não foram escritos**, e a razão é de processo, não de escopo: em
 *  18/09/2026 a adaptação deste arquivo à semente de categorias e a escrita
 *  desses três casos corriam em sessões diferentes, no mesmo working tree. A
 *  adaptação entrou primeiro (era ela que destravava a suíte) e a outra sessão
 *  encerrou antes de acrescentá-los. Fica registrado aqui porque o canal entre
 *  as duas morreu junto: quem retomar precisa descobrir pelo código.
 *
 *  **O que quem retomar precisa saber deste arquivo**, e que não é adivinhável:
 *
 *  - as fixtures são `ZUMBRA X`, `ZUMBRA Y`, `ZUMBRA W` e `KREVOL Z`, e a
 *    palavra-chave da pessoa é «zumbra». São nomes INVENTADOS, conferidos
 *    contra o motor real — uma descrição nova tem de passar pela mesma
 *    conferência antes de entrar (ver acima), ou a linha nasce categorizada
 *    pela semente e o caso perde o sujeito;
 *  - os destinos são os grupos próprios `QA Feira`, `QA Cuidado`, `QA Servico`
 *    e `QA Abrigo`, criados no primeiro teste. **Nenhum grupo da semente serve
 *    de destino**: os 14 que têm filhas não são opção do `<select>` (spec 0005
 *    §13) e não aceitam palavra-chave (§12). Um caso novo que precise de um
 *    quinto destino cria o seu, não reaproveita "Alimentação";
 *  - `QA Feira` já é dona de «zumbra» e `QA Servico` de «krevol» quando os
 *    testes 5–7 rodam. O caso 2 acima pode usar exatamente esse par;
 *  - o arquivo é `mode: 'serial'` com UMA página e UMA casa: caso novo herda o
 *    estado do anterior, e uma falha no meio esconde o tamanho do estrago —
 *    ao acrescentar, confirme que TODOS rodam, não só o último. */

test.describe.configure({ mode: 'serial' })

const CONTA = 'Conta QA Atalho'
const MES = '2026-05'

/** Os grupos PRÓPRIOS deste spec, criados sem subcategoria. São eles que
 *  aparecem como opção no `<select>` e aceitam palavra-chave; os 14 grupos da
 *  semente não fazem nem uma coisa nem outra. Ver o cabeçalho. */
const GRUPO_FEIRA = 'QA Feira'
const GRUPO_SERVICO = 'QA Servico'
const GRUPO_CUIDADO = 'QA Cuidado'
/** O grupo do caso 7: nasce FOLHA e ganha a primeira filha durante o teste. */
const GRUPO_ABRIGO = 'QA Abrigo'

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

/** Quatro linhas de maio, com nomes que a casa não reconhece — nem pelas
 *  palavras da semente, nem por palavra da pessoa, que ainda não existe. Todas
 *  entram sem categoria, que é o estado em que o atalho existe. Três dizem
 *  «zumbra» (a palavra que vai virar palavra-chave) e uma diz «krevol» (a do
 *  conflito). */
const EXTRATO: readonly Linha[] = [
  { dia: '03', valor: '-50.00', id: '01', descricao: 'ZUMBRA X' },
  { dia: '07', valor: '-33.00', id: '02', descricao: 'ZUMBRA Y' },
  { dia: '12', valor: '-22.00', id: '03', descricao: 'ZUMBRA W' },
  { dia: '20', valor: '-12.00', id: '04', descricao: 'KREVOL Z' },
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

/** A instância VISÍVEL do controle de uma linha JÁ CATEGORIZADA (emenda §19).
 *  Desde ela, o nome da categoria é o texto de um `<button>` — e são dois no
 *  DOM, um por faixa de largura, como na lacuna. */
function categoriaDaLinha(descricao: string, categoria: string): Locator {
  return page
    .getByRole('button', {
      name: new RegExp(`^${categoria}\\. Trocar categoria de ${descricao},`),
    })
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

/** Cria um GRUPO de despesa sem subcategoria: o destino selecionável e o dono
 *  de palavra-chave que os grupos da semente deixaram de ser. */
async function criarGrupoDeDespesa(nome: string): Promise<void> {
  await page.goto('/categorias')
  await page.getByRole('button', { name: 'Novo grupo', exact: true }).click()
  const dialogo = page.locator('dialog[open]')
  await dialogo.getByLabel('Nome').fill(nome)
  // Explícito, e não pelo padrão do formulário: um grupo de receita apareceria
  // no `<select>` errado e o teste morreria longe daqui.
  await dialogo.getByLabel('Natureza').selectOption('expense')
  await dialogo.getByRole('button', { name: /^Criar/ }).click()
  await expect(dialogo).toHaveCount(0)
  await expect(
    page.getByRole('region', { name: 'Despesas' }).getByText(nome, { exact: true }),
  ).toBeVisible()
}

// ------------------------------------------------------------------ cenário

test.describe('atalho de categorização em /lancamentos', () => {
  test('a conta, os grupos próprios e o extrato existem, e as quatro linhas entram sem categoria', async () => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta', exact: true }).click()
    const dialogo = page.getByRole('dialog')
    await dialogo.getByLabel('Nome').fill(CONTA)
    await dialogo.getByLabel('Tipo').selectOption('checking')
    await dialogo.getByRole('button', { name: 'Criar conta' }).click()
    await expect(page.getByRole('row', { name: new RegExp(CONTA) })).toBeVisible()

    // Os quatro grupos FOLHA do spec. A casa já tem os 15 da semente, mas 14
    // deles têm filhas e nenhum serve de destino aqui (ver o cabeçalho).
    for (const grupo of [GRUPO_FEIRA, GRUPO_SERVICO, GRUPO_CUIDADO, GRUPO_ABRIGO]) {
      await criarGrupoDeDespesa(grupo)
    }

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

    // Nenhuma linha vem sugerida — e isso agora é uma AFIRMAÇÃO SOBRE A
    // SEMENTE, não sobre uma casa vazia: as palavras de fábrica estão todas lá,
    // e nenhuma delas alcança o limiar contra `ZUMBRA` ou `KREVOL`. Se alguém
    // acrescentar à semente uma palavra que case com estes nomes, é aqui que
    // aparece.
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

    await lacuna('ZUMBRA X').click()
    const editor = editorDe('ZUMBRA X')
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

    await select.selectOption({ label: GRUPO_FEIRA })
    const [patch] = await Promise.all([
      page.waitForRequest(
        (r) => /\/transactions\/[0-9a-f-]+$/.test(r.url()) && r.method() === 'PATCH',
      ),
      editor.getByRole('button', { name: 'Categorizar', exact: true }).click(),
    ])
    // O corpo tem UM campo — o resto do PATCH é da E2b.
    expect(Object.keys(patch.postDataJSON() as Record<string, unknown>)).toEqual(['categoryId'])

    await expect(page.getByText(`Lançamento categorizado como ${GRUPO_FEIRA}.`)).toBeVisible()
    await expect(editor).toHaveCount(0)
    await expect(categoriaDaLinha('ZUMBRA X', GRUPO_FEIRA)).toHaveCount(1)
    await expect(faixaDePendencia()).toContainText('3 lançamentos de maio estão sem categoria')
    // A próxima lacuna é o próximo trabalho.
    await expect(lacuna('ZUMBRA Y')).toBeFocused()
    // E nenhuma outra linha foi tocada.
    await expect(lacuna('ZUMBRA W')).toHaveCount(1)
    await expect(lacuna('KREVOL Z')).toHaveCount(1)
  })

  test('com palavra-chave: «zumbra» entra em QA Feira e o OUTRO do mês ganha categoria', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(faixaDePendencia()).toContainText('3 lançamentos de maio estão sem categoria')

    await lacuna('ZUMBRA Y').click()
    const editor = editorDe('ZUMBRA Y')
    await editor.getByRole('combobox').selectOption({ label: GRUPO_FEIRA })

    // As fichas saem da descrição, e a pressionada se distingue sem cor: o
    // ícone troca e o rótulo do confirmar cita a palavra (itens 18 e 19).
    await expect(editor.getByText('Da próxima vez, reconhecer por')).toBeVisible()
    const ficha = editor.getByRole('button', { name: 'Reconhecer por «zumbra»', exact: true })
    await expect(ficha).toHaveAttribute('aria-pressed', 'false')
    await ficha.click()
    await expect(ficha).toHaveAttribute('aria-pressed', 'true')
    await expect(editor.getByRole('status')).toHaveText(
      `«zumbra» vira palavra-chave de ${GRUPO_FEIRA} — vale para os outros lançamentos sem categoria de maio e para as próximas importações.`,
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
    await editor.getByRole('button', { name: 'Categorizar e reconhecer por «zumbra»' }).click()

    // (a) manda a lista inteira da categoria — aqui, a primeira palavra dela.
    expect((await palavras).postDataJSON()).toEqual({ keywords: ['zumbra'] })
    expect(await lancamento).toBeTruthy()
    expect((await reprocessar).postDataJSON()).toEqual({ month: MES, dryRun: false })

    // O número do toast é o do SERVIDOR e conta só os OUTROS: ZUMBRA W. O
    // ZUMBRA X já estava categorizado e não é sobrescrito; o KREVOL Z não
    // casa com «zumbra».
    await expect(
      page.getByText(
        `«zumbra» adicionada a ${GRUPO_FEIRA} · mais 1 lançamento de maio categorizado.`,
      ),
    ).toBeVisible()
    await expect(editor).toHaveCount(0)

    await expect(categoriaDaLinha('ZUMBRA Y', GRUPO_FEIRA)).toHaveCount(1)
    // A linha que NINGUÉM tocou ganhou a categoria pelo reprocessamento.
    await expect(categoriaDaLinha('ZUMBRA W', GRUPO_FEIRA)).toHaveCount(1)
    await expect(faixaDePendencia()).toContainText('1 lançamento de maio está sem categoria')
    // A que não diz «zumbra» continua sendo a lacuna — e recebe o foco.
    await expect(lacuna('KREVOL Z')).toBeFocused()

    // A palavra ficou gravada na categoria (é o que a próxima importação usa).
    await page.goto('/categorias')
    await page.getByRole('button', { name: `Editar ${GRUPO_FEIRA}` }).click()
    await expect(
      page
        .locator('dialog[open]')
        .getByRole('list', { name: 'Palavras-chave adicionadas' })
        .getByRole('listitem'),
    ).toHaveText(['zumbra'])
    await page.keyboard.press('Escape')
  })

  test('409: a palavra já é de outra categoria — o toast diz qual e nada mais é feito', async () => {
    // QA Servico fica dona de «krevol» ANTES: é o conflito que a última linha
    // vai encontrar.
    await cadastrarPalavra(GRUPO_SERVICO, 'krevol')

    await page.goto(`/lancamentos?mes=${MES}`)
    await lacuna('KREVOL Z').click()
    const editor = editorDe('KREVOL Z')
    await editor.getByRole('combobox').selectOption({ label: GRUPO_CUIDADO })
    await editor.getByRole('button', { name: 'Reconhecer por «krevol»', exact: true }).click()

    const [resposta] = await Promise.all([
      page.waitForResponse(
        (r) => /\/categories\/[0-9a-f-]+$/.test(r.url()) && r.request().method() === 'PATCH',
      ),
      editor.getByRole('button', { name: 'Categorizar e reconhecer por «krevol»' }).click(),
    ])
    expect(resposta.status()).toBe(409)

    // A frase é nossa, com a dona pelo NOME — o `ownerId` nunca aparece.
    await expect(page.getByText(`«krevol» já está em ${GRUPO_SERVICO}.`)).toBeVisible()
    const corpo = (await resposta.json()) as { error: { fields: Record<string, string> } }
    await expect(page.getByText(corpo.error.fields.ownerId ?? 'id-que-nao-existe')).toHaveCount(0)

    // Nada mais foi feito: o editor continua aberto, a categoria escolhida
    // continua lá, a ficha saiu da linha, o rótulo voltou a `Categorizar` e o
    // foco está no confirmar.
    await expect(editor).toBeVisible()
    await expect(editor.getByRole('combobox').locator('option:checked')).toHaveText(GRUPO_CUIDADO)
    await expect(
      editor.getByRole('button', { name: 'Reconhecer por «krevol»', exact: true }),
    ).toHaveCount(0)
    await expect(editor.getByRole('button', { name: 'Categorizar', exact: true })).toBeFocused()
    // E o lançamento NÃO foi categorizado: a faixa não se mexeu.
    await expect(faixaDePendencia()).toContainText('1 lançamento de maio está sem categoria')

    // Fechar sem gravar mantém a lacuna.
    await page.keyboard.press('Escape')
    await expect(editor).toHaveCount(0)
    await expect(lacuna('KREVOL Z')).toBeFocused()
    await expect(linhaDa('KREVOL Z')).toContainText('Sem categoria')
  })

  test('teclado: Enter e Space abrem, Escape fecha e devolve o foco ao botão da célula', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)

    const botao = lacuna('KREVOL Z')
    await botao.focus()
    await expect(botao).toHaveAttribute('aria-expanded', 'false')
    await page.keyboard.press('Enter')

    const editor = editorDe('KREVOL Z')
    await expect(editor).toBeVisible()
    await expect(botao).toHaveAttribute('aria-expanded', 'true')
    await expect(botao).toHaveAttribute('aria-controls', (await editor.getAttribute('id')) ?? '')

    await page.keyboard.press('Escape')
    await expect(editor).toHaveCount(0)
    await expect(botao).toBeFocused()
    await expect(botao).toHaveAttribute('aria-expanded', 'false')

    await page.keyboard.press('Space')
    await expect(editorDe('KREVOL Z')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(botao).toBeFocused()
  })

  test('a 375 px: uma única instância do botão visível e nenhuma rolagem horizontal', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(faixaDePendencia()).toBeVisible()

    // Duas no DOM; em 1280 px a visível é a da COLUNA Categoria...
    await expect(instanciasNoDom('KREVOL Z')).toHaveCount(2)
    await expect(instanciasNoDom('KREVOL Z').filter({ visible: true })).toHaveCount(1)
    await expect(instanciasNoDom('KREVOL Z').filter({ visible: true })).toHaveAttribute(
      'id',
      /-coluna$/,
    )

    // ...e a 375 px, a da linha secundária da descrição.
    await page.setViewportSize({ width: 375, height: 800 })
    await expect(instanciasNoDom('KREVOL Z')).toHaveCount(2)
    const visivel = lacuna('KREVOL Z')
    await expect(visivel).toHaveCount(1)
    await expect(visivel).toHaveAttribute('id', /-secundaria$/)

    await visivel.click()
    const editor = editorDe('KREVOL Z')
    await expect(editor).toBeVisible()
    await editor.getByRole('combobox').selectOption({ label: GRUPO_CUIDADO })

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
    // O grupo ganha a PRIMEIRA subcategoria agora: até esta linha "QA Abrigo"
    // era folha e aceitava palavra-chave. O antes/depois é o que este caso
    // existe para provar, e é por isso que ele usa um grupo próprio: os 14
    // grupos da semente já nascem com filhas (ADR-033) e não têm "antes".
    await page.goto('/categorias')
    await page.getByRole('button', { name: `Nova subcategoria em ${GRUPO_ABRIGO}` }).click()
    await page.getByLabel('Nome').fill('Energia QA')
    await page.getByRole('button', { name: 'Criar' }).click()
    await expect(
      page.getByRole('region', { name: 'Despesas' }).getByText('Energia QA', { exact: true }),
    ).toBeVisible()

    await page.getByRole('button', { name: `Editar ${GRUPO_ABRIGO}` }).click()
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
    const abrigo = arvore.expense.find((c) => c.name === GRUPO_ABRIGO)
    expect(abrigo).toBeTruthy()
    const recusa = await page.request.patch(`/api/v1/categories/${abrigo?.id}`, {
      data: { keywords: ['zumbrete'] },
    })
    expect(recusa.status()).toBe(400)
    const erro = (await recusa.json()) as { error: { code: string; fields: Record<string, string> } }
    expect(erro.error.code).toBe('VALIDATION_FAILED')
    expect(erro.error.fields.keywords).toBe('Palavras-chave ficam nas subcategorias.')

    // O grupo sem filhas continua aceitando: a regra é sobre TER subcategoria.
    // `QA Cuidado` serve porque o 409 do caso 4 não gravou nada nele — a
    // palavra recusada nunca chegou à lista.
    const cuidado = arvore.expense.find((c) => c.name === GRUPO_CUIDADO)
    expect(cuidado).toBeTruthy()
    const aceita = await page.request.patch(`/api/v1/categories/${cuidado?.id}`, {
      data: { keywords: ['zumbrete'] },
    })
    expect(aceita.status()).toBe(200)
  })
})
