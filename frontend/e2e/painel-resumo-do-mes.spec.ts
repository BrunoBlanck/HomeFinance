import { expect, type Request, type Response, test } from '@playwright/test'

/** Ponta a ponta do painel (`/`) — a faixa de resumo do mês (E4a / spec 0008),
 *  contra a API Go real e a casa compartilhada do projeto `setup`.
 *
 *  O que só este nível prova, e que nenhum teste de componente alcança:
 *
 *  - **o aceite 18: UM pedido de rede monta a faixa.** No teste de componente
 *    o dublê da API só responde o que o autor declarou; aqui se observa o que
 *    o navegador de fato pediu. É a diferença entre afirmar que a tela não
 *    busca `/accounts` e `/categories` para decidir estado vazio e **ver** que
 *    ela não buscou — que é justamente o erro fácil, porque os dois contadores
 *    do contrato existem para evitá-lo;
 *  - **o `month=` que sai é o mês da CASCA**, não o de hoje no fuso do
 *    navegador: `?mes=` na URL, `month=` na query da API, sem nenhuma outra
 *    chave (`householdId=`, `accountId=`) tentando a sorte;
 *  - **a faixa existe com dado de verdade**, incluindo mês sem movimento: ela
 *    nunca some, e nunca troca um zero verdadeiro por travessão.
 *
 *  O spec **não afirma valores literais** — os números dependem do que os
 *  vizinhos deixaram na casa compartilhada. O que ele afirma é a FORMA: os três
 *  rótulos, nesta ordem, cada um com um valor ao lado. Valor é assunto dos
 *  testes de aceite do backend e do teste de componente.
 *
 *  **Como se conta "um pedido" aqui — e por que não é por `request` nem por
 *  `response`.** O E2E roda no dev server, e ali o `<StrictMode>` de
 *  `src/main.tsx` simula um desmonte na montagem: o TanStack Query cancela a
 *  consulta pelo `signal` (`queryFn({ signal })`) e a refaz no remonte. Medido
 *  com sonda de rede (18/09/2026): o primeiro `fetch` chega a receber os
 *  **cabeçalhos** (o Chromium emite `response`) e é abortado no corpo —
 *  `requestfailed` com `net::ERR_ABORTED`, e nunca `requestfinished`. Não é um
 *  segundo pedido, é o mesmo pedido cancelado, e em build de produção ele nem
 *  existe. O mesmo acontece com `/me`. Por isso o que se conta é o pedido
 *  **concluído** — `requestfinished`, corpo entregue —, e o evento `request`
 *  fica só para o que tem de ser **zero**: `/accounts` e `/categories` não
 *  saem nem abortados. */

/** Um mês que nenhuma outra spec toca: o vazio tem de continuar vazio, e a
 *  faixa tem de continuar lá. */
const MES_VAZIO = '2031-02'

/** O mês que `importacao.spec.ts` povoa — o único com dado garantido. */
const MES_COM_DADO = '2026-08'

/** Os três rótulos, na ordem ratificada pelo usuário em 18/09/2026. */
const ROTULOS = ['Receita do mês', 'Gasto no cartão de crédito', 'Investido no mês']

function ehPainel(url: string): boolean {
  return url.includes('/api/v1/dashboard')
}

test.describe('painel — resumo do mês', () => {
  test('um pedido a /dashboard monta a faixa, e nenhum a /accounts ou /categories', async ({
    page,
  }) => {
    // O que SAIU, para afirmar o zero: `/accounts` e `/categories` não saem
    // nunca — abortado ou não, um pedido é um pedido.
    const saidas: string[] = []
    // O que CONCLUIU, para contar: um pedido ao painel com o corpo entregue.
    // (O abortado do StrictMode nunca chega aqui — ver o cabeçalho.)
    const concluidos: string[] = []
    const aoSair = (requisicao: Request) => {
      if (requisicao.url().includes('/api/v1/')) saidas.push(requisicao.url())
    }
    const aoConcluir = (requisicao: Request) => {
      if (ehPainel(requisicao.url())) concluidos.push(requisicao.url())
    }
    page.on('request', aoSair)
    page.on('requestfinished', aoConcluir)

    try {
      const pedidoConcluido = page.waitForEvent(
        'requestfinished',
        (requisicao) => ehPainel(requisicao.url()) && requisicao.method() === 'GET',
      )
      await page.goto(`/?mes=${MES_COM_DADO}`)
      const requisicao = await pedidoConcluido
      const resposta: Response | null = await requisicao.response()
      expect(resposta?.status()).toBe(200)

      // O mês da casca chega como `month=`, e é o ÚNICO parâmetro.
      const busca = new URL(requisicao.url()).searchParams
      expect(busca.get('month')).toBe(MES_COM_DADO)
      expect([...busca.keys()]).toEqual(['month'])

      await expect(page.getByRole('heading', { level: 2, name: 'Resumo de agosto' })).toBeVisible()
      await expect(page).toHaveTitle('Painel · HomeFinance')

      // A faixa montada: os três rótulos, nesta ordem, cada um com um valor.
      const regiao = page.getByRole('region', { name: /^Resumo de / })
      await expect(regiao).toBeVisible()
      await expect(regiao.locator('dl dt')).toHaveCount(3)
      for (const [indice, rotulo] of ROTULOS.entries()) {
        await expect(regiao.locator('dl dt').nth(indice)).toContainText(rotulo)
        await expect(regiao.locator('dl dd').nth(indice)).not.toBeEmpty()
      }

      // Exatamente três números: nenhum quarto derivado (total, saldo,
      // patrimônio, variação contra o mês anterior).
      await expect(regiao.locator('dl dd')).toHaveCount(3)
    } finally {
      page.off('request', aoSair)
      page.off('requestfinished', aoConcluir)
    }

    // ——— o aceite 18, medido: um pedido concluído ao painel, e nenhum atalho.
    expect(concluidos).toHaveLength(1)
    expect(saidas.filter((url) => /\/(accounts|categories)(\?|$)/.test(url))).toEqual([])
  })

  test('trocar de mês na casca refaz o pedido — e a faixa nunca some', async ({ page }) => {
    await page.goto(`/?mes=${MES_VAZIO}`)
    await expect(page.getByRole('heading', { level: 2, name: 'Resumo de fevereiro' })).toBeVisible()

    // Mês sem movimento: a faixa CONTINUA, com os três rótulos. Sumir com ela
    // faria a pessoa achar que a tela quebrou (spec 0008 §3.4).
    const regiao = page.getByRole('region', { name: /^Resumo de / })
    await expect(regiao.locator('dl dt')).toHaveCount(3)

    const [resposta] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/v1/dashboard?month=2031-01')),
      page.getByRole('button', { name: /^Mês anterior/ }).click(),
    ])
    expect(resposta.status()).toBe(200)

    await expect(page).toHaveURL(/mes=2031-01/)
    await expect(page.getByRole('heading', { level: 2, name: 'Resumo de janeiro' })).toBeVisible()
  })

  test('o <h1> recebe o foco ao chegar pela navegação', async ({ page }) => {
    await page.goto('/contas')
    await page.getByRole('link', { name: 'Painel' }).click()

    await expect(page.getByRole('heading', { level: 1, name: /^Olá/ })).toBeFocused({
      timeout: 3_000,
    })
  })
})
