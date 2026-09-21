import { readFile } from 'node:fs/promises'
import { expect, test } from '@playwright/test'

/** Ponta a ponta da seção **Exportar** do menu IA (`/ia`, spec 0010, fatia
 *  E9a), contra a API Go real e a casa compartilhada do projeto `setup`.
 *
 *  O que só este nível prova, e que nenhum teste de componente alcança:
 *
 *  - **a minimização (aceite 7) contra dado REAL.** O teste de unidade varre o
 *    texto montado a partir de dublês: ele prova que os campos que o autor
 *    passou não saíram. Aqui o texto vem do banco, e o que se procura é o
 *    e-mail, o nome e o id de casa DESTA sessão, lidos de `/me` — se algum dia
 *    uma consulta trouxer um `JOIN` a mais, é aqui que aparece;
 *  - **o download de verdade (aceite 49).** `URL.createObjectURL` +
 *    `revokeObjectURL` no mesmo tique é o padrão que o jsdom aceita sempre e
 *    que um navegador pode cancelar. Só um navegador de verdade responde se o
 *    arquivo chega inteiro — e o conteúdo é comparado byte a byte com o que
 *    está na tela;
 *  - **a área de transferência de verdade**, com permissão concedida: o teste
 *    de componente espia `navigator.clipboard`, aqui se LÊ o que ficou nele;
 *  - **as recusas de janela pela pilha inteira** (roteador, `requireAuth`,
 *    limitador por casa, handler), e não só pelo handler em `httptest`. */

/** As nove seções normativas da §3.1 da spec 0010, na ordem normativa. */
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

function ehExportacao(url: string): boolean {
  return url.includes('/api/v1/ai/export-prompt')
}

/** A cerca de dados da seção 8 (spec 0010 + revisão de segurança da E9a): os
 *  dois marcadores são LINHA INTEIRA — é isso que o conteúdo não consegue
 *  forjar, porque nenhuma quebra de linha sobrevive a uma descrição — e
 *  carregam o mesmo nonce de 8 bytes (16 hex) desta requisição. */
const ABERTURA_DA_CERCA = /^<<<DADOS-DO-EXTRATO:([0-9a-f]{16})>>>$/m
const FIM_DA_CERCA = /^<<<FIM-DADOS-DO-EXTRATO:([0-9a-f]{16})>>>$/m

function nonceDaCerca(prompt: string): string {
  const abre = prompt.match(ABERTURA_DA_CERCA)
  const fecha = prompt.match(FIM_DA_CERCA)
  expect(abre, 'a seção 8 tem de ABRIR a cerca de dados numa linha inteira').not.toBeNull()
  expect(fecha, 'a seção 8 tem de FECHAR a cerca de dados numa linha inteira').not.toBeNull()
  expect(fecha?.[1], 'abertura e fim têm de carregar o MESMO nonce').toBe(abre?.[1])
  return abre?.[1] ?? ''
}

/** Neutraliza as DUAS coisas que variam por requisição e por desenho — o nonce
 *  da cerca e o carimbo de geração —, e nada além delas. Os dois são
 *  substituídos pelo valor literal que a própria resposta declara, e não por
 *  uma expressão genérica: uma regex de "16 hex" ou de data varreria também
 *  conteúdo da casa, e o teste passaria a tolerar diferença real. */
function normalizar(prompt: string, nonce: string, generatedAt: string): string {
  return prompt.split(nonce).join('{NONCE}').split(generatedAt).join('{GERADO-EM}')
}

test.describe('menu IA — exportar o prompt', () => {
  test('exporta, copia e baixa o MESMO texto, sem identidade nenhuma dentro dele', async ({
    page,
    context,
  }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])

    const urlsPedidas: string[] = []
    page.on('requestfinished', (req) => {
      if (ehExportacao(req.url())) urlsPedidas.push(req.url())
    })

    await page.goto('/ia')
    await expect(page.getByRole('heading', { level: 1, name: 'IA' })).toBeVisible()

    // --- a janela é UMA, no topo (aceite 47) ---------------------------------
    await expect(page.getByLabel('Período')).toHaveValue('3')
    await expect(page.getByLabel('Período')).toHaveCount(1)

    // --- o aviso vem ANTES de qualquer botão (aceite 50) ---------------------
    const aviso = page.getByText(/Este texto leva/)
    await expect(aviso).toBeVisible()
    const copiar = page.getByRole('button', { name: 'Copiar o prompt' })
    await expect(copiar).toBeVisible()
    // `DOCUMENT_POSITION_FOLLOWING` é a ordem do documento — a que o Tab e o
    // leitor de tela percorrem. "Está na tela" não bastaria: um aviso embaixo
    // do botão também está.
    const avisoVemAntes = await aviso.evaluate(
      (el, seletor) => {
        const botao = [...document.querySelectorAll('button')].find(
          (b) => b.textContent?.trim() === seletor,
        )
        if (!botao) return false
        return Boolean(el.compareDocumentPosition(botao) & Node.DOCUMENT_POSITION_FOLLOWING)
      },
      'Copiar o prompt',
    )
    expect(avisoVemAntes).toBe(true)

    // --- a janela pedida, e nada além dela, foi para a API -------------------
    //
    // Conta-se o pedido CONCLUÍDO: o dev server roda em `<StrictMode>`, e o
    // primeiro `fetch` chega a ser abortado pelo `signal` do TanStack Query no
    // remonte. Não é um segundo pedido — é o mesmo, cancelado.
    await expect.poll(() => urlsPedidas.length).toBe(1)
    const pedida = new URL(urlsPedidas[0] as string)
    const fromMonth = pedida.searchParams.get('fromMonth') ?? ''
    const toMonth = pedida.searchParams.get('toMonth') ?? ''
    expect(fromMonth).toMatch(/^\d{4}-\d{2}$/)
    expect(toMonth).toMatch(/^\d{4}-\d{2}$/)
    expect([...pedida.searchParams.keys()].sort()).toEqual(['fromMonth', 'toMonth'])

    // --- o texto: as nove seções, na ordem ----------------------------------
    await page.locator('summary').click()
    const caixa = page.getByLabel('Texto do prompt')
    await expect(caixa).toBeVisible()
    // `textContent`, e não `innerText`: o segundo normaliza espaço, e o que
    // interessa aqui é o texto EXATO que vai para o clipboard e para o arquivo.
    const naTela = await caixa.locator('pre').evaluate((el) => el.textContent ?? '')
    expect(naTela.length).toBeGreaterThan(1_000)

    let anterior = -1
    for (const titulo of SECOES) {
      const pos = naTela.indexOf(titulo)
      expect(pos, `seção ausente: ${titulo}`).toBeGreaterThan(-1)
      expect(pos, `seção fora de ordem: ${titulo}`).toBeGreaterThan(anterior)
      anterior = pos
    }
    expect(naTela).toContain('"homefinanceKeywordImport": 1')

    // --- minimização (aceite 7) contra a identidade REAL desta sessão --------
    const me = await (await page.request.get('/api/v1/me')).json()
    for (const [oQue, agulha] of [
      ['e-mail do usuário', me.user.email],
      ['nome do usuário', me.user.name],
      ['id do usuário', me.user.id],
      ['id da casa', me.household.id],
      ['nome da casa', me.household.name],
    ] as const) {
      expect(naTela, `minimização: ${oQue} não pode estar no prompt`).not.toContain(agulha)
    }
    // A varredura por `@` fica no teste de unidade, onde a fixture é
    // controlada: aqui a casa é compartilhada e uma DESCRIÇÃO importada pode
    // legitimamente conter um `@` (`PIX FULANO@...`) — proibir o caractere
    // neste nível seria proibir o conteúdo que o prompt existe para levar.

    // --- copiar entrega EXATAMENTE o texto exibido (aceite 49, metade 1) -----
    await copiar.click()
    await expect(page.getByText('Copiado.')).toBeVisible()
    const noClipboard = await page.evaluate(() => navigator.clipboard.readText())
    // MEDIDO em 21/09/2026: o que sai do clipboard no **Windows** vem com
    // `\r\n` onde o app escreveu `\n` — é a área de transferência do sistema
    // operacional que normaliza a quebra de linha ao entregar texto, não a
    // tela. Comparar cru faria este teste passar no Linux e falhar no Windows
    // por um motivo que não é do produto. O que o aceite 49 exige, e o que se
    // afirma aqui, é que o CONTEÚDO seja o mesmo.
    expect(noClipboard.replace(/\r\n/g, '\n')).toBe(naTela)

    // --- baixar entrega o MESMO conteúdo (aceite 49, metade 2) ---------------
    //
    // Este é o passo que o jsdom não consegue dar: o `<pre>` some do caminho, o
    // Blob atravessa o navegador de verdade e o `revokeObjectURL` acontece no
    // mesmo tique do clique. Se o arquivo chegar truncado ou não chegar, é
    // aqui.
    const [baixado] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: 'Baixar .md' }).click(),
    ])
    expect(baixado.suggestedFilename()).toBe(`homefinance-prompt-${fromMonth}-a-${toMonth}.md`)
    const caminho = await baixado.path()
    expect(caminho).not.toBeNull()
    expect(await readFile(caminho as string, 'utf-8')).toBe(naTela)
  })

  /** As recusas de janela pela pilha inteira — roteador, `requireAuth`,
   *  limitador por casa e handler —, e não só pelo handler em `httptest`.
   *
   *  O caso do parâmetro REPETIDO só existe de verdade aqui: `httptest` monta a
   *  `url.Values` a partir da string, mas é neste nível que se vê que nada
   *  entre o navegador e o handler (proxy do Vite inclusive) colapsou a chave
   *  repetida em uma antes de o `SoleQueryValue` poder recusá-la. */
  test('recusa a janela longa, a invertida, a malformada e a repetida — sempre 400', async ({
    page,
  }) => {
    const casos = [
      { nome: 'quatro meses', query: 'fromMonth=2026-06&toMonth=2026-09', campo: 'toMonth' },
      { nome: 'invertida', query: 'fromMonth=2026-09&toMonth=2026-07', campo: 'toMonth' },
      { nome: 'from malformado', query: 'fromMonth=2026-7&toMonth=2026-09', campo: 'fromMonth' },
      { nome: 'to sem os dois dígitos', query: 'fromMonth=2026-07&toMonth=setembro', campo: 'toMonth' },
      { nome: 'from ausente', query: 'toMonth=2026-09', campo: 'fromMonth' },
      {
        nome: 'fromMonth repetido com valores diferentes',
        query: 'fromMonth=2026-07&fromMonth=2026-01&toMonth=2026-09',
        campo: 'fromMonth',
      },
      {
        nome: 'fromMonth repetido com o MESMO valor',
        query: 'fromMonth=2026-07&fromMonth=2026-07&toMonth=2026-09',
        campo: 'fromMonth',
      },
      {
        nome: 'toMonth com a segunda ocorrência vazia',
        query: 'fromMonth=2026-07&toMonth=2026-09&toMonth=',
        campo: 'toMonth',
      },
    ]

    for (const caso of casos) {
      const resposta = await page.request.get(`/api/v1/ai/export-prompt?${caso.query}`)
      expect(resposta.status(), caso.nome).toBe(400)
      const corpo = await resposta.json()
      expect(corpo.error.code, caso.nome).toBe('VALIDATION_FAILED')
      expect(Object.keys(corpo.error.fields ?? {}), caso.nome).toEqual([caso.campo])
      // A recusa não ecoa o que foi mandado: devolver a entrada pela porta do
      // erro é devolver entrada de terceiro.
      expect(await resposta.text(), caso.nome).not.toContain('2026-01')
    }
  })

  /** BOLA pela pilha real: a casa vem do TOKEN. Um `householdId` na query é
   *  ignorado sem efeito — nada no handler o lê —, e o CONTEÚDO da resposta é o
   *  mesmo da requisição sem ele.
   *
   *  **Por que não é mais byte a byte.** Duas respostas desta rota nunca são
   *  idênticas, e isso é o desenho, não um defeito: a cerca de dados da seção 8
   *  carrega um nonce de 8 bytes sorteado por REQUISIÇÃO (a defesa contra
   *  injeção de prompt), e o carimbo `gerado em` tem resolução de segundo, então
   *  duas chamadas seguidas podem cair em segundos diferentes. As duas variam
   *  por motivo legítimo e conhecido; o resto **não pode** variar.
   *
   *  Então o teste normaliza exatamente esses dois — e nada além deles — e
   *  aproveita para AFIRMAR que o nonce muda, em vez de só tolerar que mude:
   *  um nonce fixo entre requisições seria uma cerca que quem já viu um prompt
   *  desta casa consegue forjar. */
  test('householdId na query não muda o conteúdo, e o nonce da cerca muda', async ({ page }) => {
    const alvo = '/api/v1/ai/export-prompt?fromMonth=2026-08&toMonth=2026-09'
    const limpa = await page.request.get(alvo)
    expect(limpa.status()).toBe(200)
    const suja = await page.request.get(`${alvo}&householdId=0000&accountId=0000`)
    expect(suja.status()).toBe(200)

    const a = await limpa.json()
    const b = await suja.json()

    // A cerca existe, é uma linha inteira, e abertura e fim carregam o MESMO
    // nonce — em cada uma das duas respostas.
    const nonceA = nonceDaCerca(a.prompt)
    const nonceB = nonceDaCerca(b.prompt)
    expect(nonceA).toMatch(/^[0-9a-f]{16}$/)
    expect(nonceB).toMatch(/^[0-9a-f]{16}$/)
    expect(nonceB, 'o nonce tem de ser sorteado por requisição').not.toBe(nonceA)

    // Com o nonce e o carimbo neutralizados, o texto tem de ser o MESMO: se o
    // `householdId` tivesse qualquer efeito, ele apareceria aqui.
    expect(normalizar(b.prompt, nonceB, b.generatedAt)).toBe(
      normalizar(a.prompt, nonceA, a.generatedAt),
    )
    // E as contagens em separado, sem nada para normalizar.
    expect(b.stats).toEqual(a.stats)
  })
})
