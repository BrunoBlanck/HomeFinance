import { expect, type Locator, type Page, test } from '@playwright/test'
import { BASE_URL } from './support/ambiente'
import { entrarComContaNova } from './support/sessao'

/** Ponta a ponta do filtro de tipo de `/lancamentos` (E2d, spec 0004 §12.5.11),
 *  contra a API Go real.
 *
 *  O que só este nível prova, e que nenhum teste de componente alcança:
 *
 *  - **o `kindGroup` que a tela manda é o que o servidor entende.** Um erro de
 *    nome aqui é silencioso por construção: `kindGroup=despesas` faria a API
 *    ignorar o parâmetro desconhecido e devolver a janela inteira, enquanto o
 *    seletor continuaria afirmando "Despesas". Com `fetch` dublê, quem responde
 *    é o autor do teste; aqui é o banco;
 *  - **`investedCents` e `redeemedCents` não respondem ao filtro** (ADR-030 c′).
 *    A 2ª linha da faixa sobrevive sob `?tipo=despesas` porque o servidor manda
 *    os dois números iguais nas cinco opções — é promessa de contrato, e é aqui
 *    que ela encosta no SQL de verdade;
 *  - **`Entrou` sob receitas é idêntico ao `Entrou` de Tudo.** Um número que
 *    mudasse ao filtrar denunciaria dupla contagem, e as duas leituras saem da
 *    mesma consulta agregada;
 *  - **uma URL colada não vira varredura do mês.** A contagem de requisições de
 *    `/transactions` é medida no navegador, com a rede real.
 *
 *  **Casa PRÓPRIA**, e não a compartilhada do projeto `setup`: esta spec afirma
 *  totais do mês inteiro, e um lançamento que outra spec deixasse ali entraria
 *  na soma. Roda em série — os testes contam a mesma história em ordem. */

test.describe.configure({ mode: 'serial' })

const CONTA = 'Conta QA Filtro'
/** Mês próprio, para o mês não ter nada além do que este arquivo importou. */
const MES = '2026-11'

type Linha = { dia: string; valor: string; id: string; descricao: string }

/** Um mês com tudo o que o filtro particiona: duas despesas comuns, uma
 *  receita, um aporte e um resgate.
 *
 *  As duas despesas e a receita ficam SEM categoria de propósito: é delas que
 *  vive a faixa de pendência, que sob filtro precisa dizer "2 despesas" e
 *  "1 receita" em vez de "3 lançamentos".
 *
 *  **Por isso as três têm nome inventado** (ADR-033, 18/09/2026). Chamavam-se
 *  `SALARIO`, `MERCADO` e `PADARIA`; com a semente de categorias, casa nova
 *  reconhece as três de fábrica e a faixa de pendência sumiria do mês inteiro.
 *  `TARVIN`, `DORNEK` e `MULFAZ` foram conferidos contra o motor real
 *  (`internal/textmatch` + `internal/category/seed.go`): nenhum alcança o
 *  limiar de 80 em nenhum dos dois lados do dinheiro. As duas linhas que
 *  PRECISAM ser reconhecidas — o aporte e o resgate — continuam dizendo `CDB`
 *  e `RESGATE CDB`, que são palavras da semente. */
const EXTRATO: readonly Linha[] = [
  { dia: '05', valor: '-2000.00', id: '01', descricao: 'CDB 15 DIAS QA' },
  { dia: '10', valor: '5300.00', id: '02', descricao: 'TARVIN QA FILTRO' },
  { dia: '12', valor: '850.00', id: '03', descricao: 'RESGATE CDB QA' },
  { dia: '20', valor: '-300.00', id: '04', descricao: 'DORNEK QA FILTRO' },
  { dia: '22', valor: '-100.00', id: '05', descricao: 'MULFAZ QA FILTRO' },
]

function extratoCSV(linhas: readonly Linha[], mes: string): Buffer {
  const cabecalho = 'Data,Valor,Identificador,Descrição\n'
  const corpo = linhas
    .map(
      (l) =>
        `${l.dia}/${mes}/2026,${l.valor},44444444-4444-4444-8444-4444444444${l.id},${l.descricao}\n`,
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
  await entrarComContaNova(page, 'QA Filtro')
})

test.afterAll(async () => {
  await page
    ?.context()
    .close()
    .catch(() => {})
})

// ------------------------------------------------------------- utilitários

/** Um item da faixa do mês, pelo rótulo que o abre.
 *
 *  Três precisões, cada uma por causa de um falso positivo real:
 *
 *  - **regex, e não string**: a busca por texto do Playwright é insensível a
 *    caixa, e `'Saiu'` casaria também a linha de apoio ("O que saiu em
 *    novembro.");
 *  - **ancorado no início**: o `em aportes` da 2ª linha mora num `<span>` que
 *    começa pelo número, e o `Aportes` da faixa num que começa pelo rótulo;
 *  - **restrito ao `<span>` dentro do `<p>`**: o `caption` `sr-only` da tabela
 *    diz "Aportes e resgates de novembro…", e para um seletor ele é um
 *    elemento como outro qualquer.
 *
 *  Cada item traz o rótulo e o número juntos — por isso `toContainText` basta
 *  para conferir os dois de uma vez. */
function itemDaFaixa(pagina: Page, rotulo: string): Locator {
  return pagina.locator('p > span').filter({ hasText: new RegExp(`^${rotulo}\\b`) })
}

/** A 2ª linha da faixa: a que nomeia o que ficou de fora dos números. */
function segundaLinhaDaFaixa(pagina: Page): Locator {
  return pagina.getByText('Fora destes números:').locator('xpath=..')
}

/** As requisições de LISTA feitas durante `acao`, na ordem.
 *
 *  A assinatura da amplificação é o `cursor=`: sem a guarda, a tela pede página
 *  após página até o fim do mês, e da segunda em diante todas o carregam.
 *  Contar requisições cruas diria menos — o recarregamento da página e o
 *  `refetch` ao ganhar foco também contam, e nenhum dos dois é o defeito. */
async function listasDurante(pagina: Page, acao: () => Promise<void>): Promise<string[]> {
  const urls: string[] = []
  const ouvinte = (requisicao: { url(): string }) => {
    if (requisicao.url().includes('/api/v1/transactions?')) urls.push(requisicao.url())
  }
  pagina.on('request', ouvinte)
  try {
    await acao()
  } finally {
    pagina.off('request', ouvinte)
  }
  return urls
}

// ------------------------------------------------------------------ testes

test.describe('filtro de tipo em /lancamentos', () => {
  test('a conta e o extrato do mês, com a semente reconhecendo o aporte e o resgate', async () => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta', exact: true }).click()
    const dialogo = page.locator('dialog[open]')
    await dialogo.getByLabel('Nome').fill(CONTA)
    await dialogo.getByLabel('Tipo').selectOption('checking')
    await dialogo.getByRole('button', { name: 'Criar conta' }).click()
    await expect(page.getByRole('row', { name: new RegExp(CONTA) })).toBeVisible()

    // Nenhuma palavra-chave é cadastrada aqui, e isso é a mudança de 18/09/2026
    // (ADR-033): até então este teste punha «cdb» em "Investimentos" e
    // «resgate» em "Resgates". Os dois passos morreram juntos — os grupos da
    // semente ganharam filhas, então o diálogo deles esconde o campo de
    // palavras-chave (spec 0005 §12), e «cdb» já é da folha
    // "Investimentos › Renda fixa e Tesouro Direto" (409 KEYWORD_TAKEN).
    //
    // O que o teste precisa continua valendo, agora de fábrica: `CDB 15 DIAS QA`
    // casa «cdb» (100, lado da despesa) e `RESGATE CDB QA` casa «resgate cdb»
    // (100, lado da receita) — conferido contra o motor real.

    await page.goto('/importar')
    await page.getByLabel('Conta de destino').selectOption({ label: CONTA })
    await page.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
      name: 'NU_2026-11.csv',
      mimeType: 'text/csv',
      buffer: extratoCSV(EXTRATO, '11'),
    })
    await page.getByRole('button', { name: 'Analisar arquivo' }).click()
    await expect(
      page.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
    ).toBeVisible()
    await page.getByRole('button', { name: `Importar ${EXTRATO.length} lançamento` }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Importação concluída' })).toBeVisible()
  })

  test('em Tudo, a faixa tem os três números e a segunda linha nomeia o que saiu', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(page.getByRole('heading', { level: 1, name: 'Lançamentos' })).toBeVisible()
    await expect(page).toHaveTitle('Lançamentos · HomeFinance')
    await expect(page.getByText('Tudo o que entrou e saiu em novembro.')).toBeVisible()

    // Receita já sem o resgate; despesa já sem o aporte (ADR-029e).
    await expect(itemDaFaixa(page, 'Entrou')).toContainText('5.300,00')
    await expect(itemDaFaixa(page, 'Saiu')).toContainText('400,00')
    await expect(itemDaFaixa(page, 'Resultado')).toContainText('4.900,00')

    await expect(segundaLinhaDaFaixa(page)).toContainText('em aportes')
    await expect(segundaLinhaDaFaixa(page)).toContainText('em resgates')

    // As cinco linhas estão na tela, e a pendência é a do mês inteiro.
    await expect(page.getByRole('row', { name: /TARVIN QA FILTRO/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /CDB 15 DIAS QA/ })).toBeVisible()
    await expect(page.getByText(/3 lançamentos de novembro estão sem categoria/)).toBeVisible()
  })

  test('trocar o seletor para Despesas filtra no servidor e muda a URL', async () => {
    await page.goto(`/lancamentos?mes=${MES}`)
    await expect(page.getByRole('row', { name: /TARVIN QA FILTRO/ })).toBeVisible()

    // O foco vai para o controle ANTES da troca, como acontece com quem usa o
    // teclado: `selectOption` sozinho não foca o elemento, e sem isto o teste
    // provaria o comportamento do Playwright, não o da tela.
    await page.getByLabel('Tipo').focus()
    await page.getByLabel('Tipo').selectOption('despesas')

    await expect(page).toHaveURL(/tipo=despesas/)
    await expect(page).toHaveTitle('Despesas · Lançamentos · HomeFinance')
    await expect(page.getByText('O que saiu em novembro.')).toBeVisible()
    // O foco não sai do controle que a pessoa acabou de usar.
    await expect(page.getByLabel('Tipo')).toBeFocused()

    // A receita e o resgate saíram — e o aporte também: ele não é despesa.
    await expect(page.getByRole('row', { name: /TARVIN QA FILTRO/ })).toHaveCount(0)
    await expect(page.getByRole('row', { name: /RESGATE CDB QA/ })).toHaveCount(0)
    await expect(page.getByRole('row', { name: /CDB 15 DIAS QA/ })).toHaveCount(0)
    await expect(page.getByRole('row', { name: /DORNEK QA FILTRO/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /MULFAZ QA FILTRO/ })).toBeVisible()

    // Um número só: `Entrou` seria um zero que nunca muda, e `Resultado`
    // repetiria `Saiu` com o sinal trocado.
    await expect(itemDaFaixa(page, 'Saiu')).toContainText('400,00')
    await expect(itemDaFaixa(page, 'Entrou')).toHaveCount(0)
    await expect(itemDaFaixa(page, 'Resultado')).toHaveCount(0)

    // A 2ª linha SOBREVIVE ao filtro, citando só o lado do dinheiro que ele
    // nomeia — e isso só é possível porque o servidor mantém `investedCents`
    // igual nas cinco opções.
    await expect(segundaLinhaDaFaixa(page)).toContainText('2.000,00')
    await expect(segundaLinhaDaFaixa(page)).toContainText('em aportes')
    await expect(segundaLinhaDaFaixa(page)).not.toContainText('em resgates')

    // A faixa de pendência nomeia o tipo e concorda em gênero.
    await expect(page.getByText(/2 despesas de novembro estão sem categoria/)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Ver só essas 2' })).toBeVisible()
    await expect(
      page.getByRole('button', { name: 'Categorizar o mês automaticamente' }),
    ).toBeVisible()
  })

  test('a URL colada com ?tipo=despesas abre no mesmo estado', async () => {
    await page.goto(`/lancamentos?mes=${MES}&tipo=despesas`)

    await expect(page.getByRole('heading', { level: 1, name: 'Lançamentos' })).toBeVisible()
    await expect(page).toHaveTitle('Despesas · Lançamentos · HomeFinance')
    await expect(page.getByLabel('Tipo')).toHaveValue('despesas')
    await expect(page.getByRole('row', { name: /DORNEK QA FILTRO/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /TARVIN QA FILTRO/ })).toHaveCount(0)
    await expect(
      page.getByRole('table', { name: 'Despesas de novembro, agrupadas por dia' }),
    ).toBeVisible()
  })

  test('em Receitas, Entrou é IDÊNTICO ao de Tudo e a 2ª linha cita só os resgates', async () => {
    await page.goto(`/lancamentos?mes=${MES}&tipo=receitas`)
    await expect(page.getByRole('row', { name: /TARVIN QA FILTRO/ })).toBeVisible()

    // O corte do ADR-029(e) já estava aplicado ANTES do filtro: um número que
    // mudasse aqui denunciaria dupla contagem.
    await expect(itemDaFaixa(page, 'Entrou')).toContainText('5.300,00')
    await expect(itemDaFaixa(page, 'Saiu')).toHaveCount(0)
    await expect(itemDaFaixa(page, 'Resultado')).toHaveCount(0)

    await expect(segundaLinhaDaFaixa(page)).toContainText('850,00')
    await expect(segundaLinhaDaFaixa(page)).toContainText('em resgates')
    await expect(segundaLinhaDaFaixa(page)).not.toContainText('em aportes')

    // O resgate NÃO é receita: ele está em Investimentos.
    await expect(page.getByRole('row', { name: /RESGATE CDB QA/ })).toHaveCount(0)
    await expect(page.getByText(/1 receita de novembro está sem categoria/)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Ver essa receita' })).toBeVisible()
  })

  test('em Investimentos a palavra entra na linha e o valor perde cor e sinal', async () => {
    await page.goto(`/lancamentos?mes=${MES}&tipo=investimentos`)
    await expect(page).toHaveTitle('Investimentos · Lançamentos · HomeFinance')
    await expect(
      page.getByText('O que saiu para investir e o que voltou em novembro.'),
    ).toBeVisible()

    await expect(itemDaFaixa(page, 'Aportes')).toContainText('2.000,00')
    await expect(itemDaFaixa(page, 'Resgates')).toContainText('850,00')
    await expect(itemDaFaixa(page, 'Resultado')).toHaveCount(0)
    await expect(page.getByText('Fora destes números:')).toHaveCount(0)

    const aporte = page.getByRole('row', { name: /CDB 15 DIAS QA/ })
    await expect(aporte).toContainText('Aporte')
    // Sem o `−`: marcar o aporte como saída ensina que poupar é prejuízo — o
    // erro que a E7 existe para corrigir.
    await expect(aporte).not.toContainText('-2.000,00')
    await expect(page.getByRole('row', { name: /RESGATE CDB QA/ })).toContainText('Resgate')
    // As despesas comuns e a receita não estão aqui.
    await expect(page.getByRole('row', { name: /DORNEK QA FILTRO/ })).toHaveCount(0)
    // E não há pendência: aporte e resgate têm categoria por definição.
    await expect(page.getByText(/estão sem categoria/)).toHaveCount(0)
  })

  test('em Transferências a faixa é uma frase, e o vazio oferece a saída', async () => {
    await page.goto(`/lancamentos?mes=${MES}&tipo=transferencias`)

    await expect(
      page.getByText(
        'Transferência não é receita nem despesa — o dinheiro só mudou de conta dentro da casa.',
      ),
    ).toBeVisible()
    await expect(itemDaFaixa(page, 'Entrou')).toHaveCount(0)
    await expect(itemDaFaixa(page, 'Resultado')).toHaveCount(0)
    // Este mês não tem transferência: o vazio orienta, e o botão diz o que limpa.
    await expect(page.getByText('Nenhuma transferência em novembro.')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Mostrar todos os tipos' })).toBeVisible()
  })

  /** A tempestade de requisições. `?tipo=transferencias&semCategoria=1` é uma
   *  combinação sem resultado possível — sem o descarte no portão da busca, a
   *  tela varreria o mês inteiro, 50 linhas por requisição, atrás de uma linha
   *  que não pode existir. */
  test('?tipo=transferencias&semCategoria=1 abre sem o filtro de pendência e sem varrer o mês', async () => {
    const listas = await listasDurante(page, async () => {
      await page.goto(`/lancamentos?mes=${MES}&tipo=transferencias&semCategoria=1`)
      await expect(page.getByText('Nenhuma transferência em novembro.')).toBeVisible()
    })

    // Nenhuma página seguinte foi pedida — e o filtro de pendência, que é o que
    // dispararia o laço, nem chegou a ligar.
    expect(listas.filter((url) => url.includes('cursor='))).toEqual([])
    expect(listas.every((url) => url.includes('kindGroup=transfer'))).toBe(true)
    await expect(page.getByText(/Mostrando só/)).toHaveCount(0)
  })


  test('?tipo com lixo abre em Tudo, sem erro e sem mandar nada à API', async () => {
    const pedidos: string[] = []
    const ouvinte = (requisicao: { url(): string }) => {
      if (requisicao.url().includes('/api/v1/transactions?')) pedidos.push(requisicao.url())
    }
    page.on('request', ouvinte)
    try {
      await page.goto(`/lancamentos?mes=${MES}&tipo=${encodeURIComponent("' OR 1=1 --")}`)
      await expect(page.getByRole('row', { name: /TARVIN QA FILTRO/ })).toBeVisible()
    } finally {
      page.off('request', ouvinte)
    }

    await expect(page).toHaveTitle('Lançamentos · HomeFinance')
    await expect(page.getByLabel('Tipo')).toHaveValue('')
    expect(pedidos.some((url) => url.includes('kindGroup'))).toBe(false)
    expect(pedidos.some((url) => url.includes('1%3D1') || url.includes('1=1'))).toBe(false)
  })
})
