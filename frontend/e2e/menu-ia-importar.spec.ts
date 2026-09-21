import { expect, type Locator, type Page, test } from '@playwright/test'
import { API_URL } from './support/ambiente'

/** Ponta a ponta da seção **Importar** do menu IA (`/ia`, spec 0010, fatia
 *  E9b), contra a API Go real e a casa compartilhada do projeto `setup`.
 *
 *  O que só este nível prova:
 *
 *  - **o JSON atravessa a pilha inteira e o resultado está nas OUTRAS telas**:
 *    a categoria criada aparece em `/categorias` com as palavras dela no
 *    diálogo; a desmarcada não existe; a palavra de conta aparece no diálogo
 *    da conta em `/contas` (aceite 48 provado no banco, e não no rótulo);
 *  - **o confirm manda o MESMO payload da prévia** mais os `ref` que o
 *    servidor devolveu — o corpo é lido na rede, não no estado do React;
 *  - **`notes` morre no servidor**: colado com um marcador, ele não volta na
 *    resposta nem aparece no DOM (aceite 21);
 *  - **a prévia não grava**: entre Conferir e Confirmar, `/categorias` não
 *    tem a categoria nova.
 *
 *  A casa é compartilhada com os outros specs (ver `sessao.setup.ts`); todo
 *  nome e toda palavra são próprios deste spec (`QA IA …`, `… ia`), e as
 *  palavras usam os radicais de fixture que a semente não reconhece. */

const GRUPO_ALVO = 'QA IA Loja' // grupo sem filhas: dono de palavras de categoria
const CONTA_ALVO = 'QA IA Conta'
const GRUPO_NOVO = 'QA IA Grupo Novo'
const FOLHA_CRIADA = 'QA IA Criada'
const FOLHA_DESMARCADA = 'QA IA Desmarcada'
const MARCADOR_NOTAS = 'MARCADOR-NOTAS-E2E-xyzzy'

type CategoriaJSON = { id: string; name: string; children?: CategoriaJSON[] }
type ArvoreJSON = Record<'expense' | 'income' | 'investment' | 'redemption', CategoriaJSON[]>

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

/** Abre o diálogo de edição de uma categoria em `/categorias` e devolve as
 *  fichas de palavra-chave dele. Fecha com Escape depois. */
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

/** O marcador de `notes` pode estar em UM lugar: no `<textarea>`, que é o
 *  que a pessoa colou. Em qualquer outro nó do DOM ele seria o servidor (ou
 *  a tela) reproduzindo texto livre da IA. */
async function marcadorForaDoCampo(page: Page, marcador: string): Promise<boolean> {
  return page.evaluate((m) => {
    const clone = document.body.cloneNode(true) as HTMLElement
    for (const campo of clone.querySelectorAll('textarea')) campo.remove()
    return clone.textContent?.includes(m) ?? false
  }, marcador)
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

test.describe.configure({ mode: 'serial' })

test.describe('menu IA — importar o JSON da IA', () => {
  // Erro de JS não tratado derruba a árvore do React e deixa a página em
  // branco; sem isto o sintoma seria só "botão não encontrado" 60 s depois.
  test.beforeEach(({ page }) => {
    page.on('pageerror', (erro) => {
      throw new Error(`erro de página não tratado: ${erro.message}
${erro.stack ?? ''}`)
    })
    page.on('console', (msg) => {
      if (msg.type() === 'error') console.log(`[console.error] ${msg.text()}`)
    })
  })

  test('cola, confere, desmarca uma categoria, confirma — e o resultado está em /categorias e /contas', async ({
    page,
  }) => {
    // --- preparação: um grupo sem filhas e uma conta, pelas telas ------------
    await criarGrupoDeDespesa(page, GRUPO_ALVO)
    await criarConta(page, CONTA_ALVO)

    const arvoreAntes = (await (await page.request.get('/api/v1/categories')).json()) as ArvoreJSON
    const loja = categoriaNaArvore(arvoreAntes, GRUPO_ALVO)
    expect(loja, 'o grupo alvo existe').toBeDefined()
    const contas = (await (await page.request.get('/api/v1/accounts')).json()) as {
      items: Array<{ id: string; name: string }>
    }
    const conta = contas.items.find((c) => c.name === CONTA_ALVO)
    expect(conta, 'a conta alvo existe').toBeDefined()

    // O JSON "da IA": categoria nova em grupo novo (duas folhas, uma vai ser
    // desmarcada), palavra de categoria num item existente (repetida de
    // propósito: deduplica em silêncio), palavra de conta, e `notes` com um
    // marcador que NÃO pode reaparecer.
    const payload = {
      homefinanceKeywordImport: 1,
      newCategories: [
        { group: GRUPO_NOVO, name: FOLHA_CRIADA, kind: 'expense', add: ['zumbra ia'] },
        { group: GRUPO_NOVO, name: FOLHA_DESMARCADA, add: ['krevol ia', 'krevol ia dois'] },
      ],
      categoryKeywords: [
        {
          categoryId: loja?.id,
          categoryPath: GRUPO_ALVO,
          add: ['plintaq ia', 'PLINTAQ IA'],
        },
      ],
      accountKeywords: [{ accountId: conta?.id, accountName: CONTA_ALVO, add: ['vrandix ia'] }],
      notes: `${MARCADOR_NOTAS}: a IA explicando o que fez`,
    }

    // Os corpos que saem para a API, lidos na rede.
    const corpos: Array<{ rota: string; corpo: unknown }> = []
    const respostas: string[] = []
    page.on('request', (req) => {
      if (req.url().includes('/api/v1/ai/keyword-import/') && req.method() === 'POST') {
        corpos.push({ rota: req.url().split('/api/v1')[1] ?? '', corpo: req.postDataJSON() })
      }
    })
    page.on('response', async (res) => {
      if (res.url().includes('/api/v1/ai/keyword-import/')) respostas.push(await res.text())
    })

    await page.goto('/ia')
    await expect(page.getByRole('heading', { level: 1, name: 'IA' })).toBeVisible()

    // --- vazia: o botão diz o que falta e não é `disabled` -------------------
    const botaoVazio = page.getByRole('button', { name: 'Cole o JSON para conferir' })
    await expect(botaoVazio).toBeVisible()
    await expect(botaoVazio).toHaveAttribute('aria-disabled', 'true')
    // `aria-disabled`, nunca o atributo `disabled`: o botão continua focável.
    await expect(botaoVazio).not.toHaveAttribute('disabled')

    // --- cola e confere -------------------------------------------------------
    //
    // Escopado à seção 2 desde a E9c: a seção 3 (Reprocessar) também tem um
    // botão "Conferir", e `page.getByRole` sozinho resolvia para dois
    // elementos (regressão achada pelo QA em 21/09/2026).
    const importar = page.getByRole('region', { name: '2 · Importar o que a IA respondeu' })
    await importar
      .getByLabel('Cole aqui o JSON que a IA respondeu')
      .fill(JSON.stringify(payload, null, 2))
    await importar.getByRole('button', { name: 'Conferir', exact: true }).click()

    // Bloco A: as duas categorias, MARCADAS por padrão (aceite 48).
    await expect(page.getByRole('heading', { name: 'Categorias a criar · 2' })).toBeVisible()
    const caixaCriada = page.getByRole('checkbox', {
      name: `Criar ${GRUPO_NOVO} > ${FOLHA_CRIADA} com 1 palavra-chave`,
    })
    const caixaDesmarcada = page.getByRole('checkbox', {
      name: `Criar ${GRUPO_NOVO} > ${FOLHA_DESMARCADA} com 2 palavras-chave`,
    })
    await expect(caixaCriada).toBeChecked()
    await expect(caixaDesmarcada).toBeChecked()

    // Bloco B: a palavra de conta com o impacto medido (uma linha por palavra).
    await expect(page.getByRole('heading', { name: 'Palavras-chave de conta · 1' })).toBeVisible()
    await expect(page.getByText('«vrandix ia»').first()).toBeVisible()

    // Bloco C: a palavra de categoria, UMA vez (a repetida foi deduplicada).
    await expect(
      page.getByRole('heading', { name: 'Palavras-chave de categoria · 1' }),
    ).toBeVisible()

    // Totais e rótulo com tudo marcado: 2 categorias, 1 + 2 + 1 + 1 = 5 palavras.
    const status = page.getByRole('status').filter({ hasText: /palavras entram/ })
    await expect(status).toHaveText('2 categorias novas · 5 palavras entram.')
    const confirmar = page.getByRole('button', { name: 'Criar 2 categorias e gravar 5 palavras' })
    await expect(confirmar).toBeVisible()
    await expect(page.locator('output').filter({ hasText: /grupo novo/ })).toHaveText('Inclui 1 grupo novo.')

    // `notes` não voltou na resposta e não está no DOM fora do campo (aceite 21).
    expect(respostas).toHaveLength(1)
    expect(respostas[0]).not.toContain(MARCADOR_NOTAS)
    expect(respostas[0]).not.toContain('notes')
    expect(await marcadorForaDoCampo(page, MARCADOR_NOTAS)).toBe(false)

    // A prévia NÃO gravou: a árvore da casa é a mesma (aceite 11).
    const arvoreDepoisDaPrevia = (await (
      await page.request.get('/api/v1/categories')
    ).json()) as ArvoreJSON
    expect(categoriaNaArvore(arvoreDepoisDaPrevia, FOLHA_CRIADA)).toBeUndefined()
    expect(categoriaNaArvore(arvoreDepoisDaPrevia, GRUPO_NOVO)).toBeUndefined()

    // --- desmarca UMA categoria: o rótulo cai 1 categoria e 2 palavras (aceite 48)
    await caixaDesmarcada.uncheck()
    await expect(caixaDesmarcada).not.toBeChecked()
    await expect(caixaCriada).toBeChecked()
    await expect(status).toHaveText('1 categoria nova · 3 palavras entram.')
    const confirmarAjustado = page.getByRole('button', {
      name: 'Criar 1 categoria e gravar 3 palavras',
    })
    await expect(confirmarAjustado).toBeVisible()

    // --- confirma --------------------------------------------------------------
    await confirmarAjustado.click()
    await expect(page.getByRole('status').filter({ hasText: /gravadas?\./ })).toHaveText(
      '1 categoria criada e 3 palavras gravadas.',
    )
    await expect(page.getByText('Desmarcadas por você')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Ir para Reprocessar' })).toBeVisible()
    await expect.poll(() => respostas.length).toBe(2)
    expect(respostas[1]).not.toContain(MARCADOR_NOTAS)
    // O campo foi limpo pelo confirm: agora o DOM inteiro tem de estar limpo.
    expect(await page.content()).not.toContain(MARCADOR_NOTAS)

    // O confirm levou o MESMO payload da prévia (com `notes` e tudo — quem o
    // descarta é o servidor) mais o `ref` que o servidor devolveu.
    expect(corpos.map((c) => c.rota)).toEqual([
      '/ai/keyword-import/preview',
      '/ai/keyword-import/confirm',
    ])
    const previa = corpos[0]?.corpo as { payload: unknown; skipNewCategories?: string[] }
    const confirm = corpos[1]?.corpo as { payload: unknown; skipNewCategories?: string[] }
    expect(confirm.payload).toEqual(previa.payload)
    expect(confirm.payload).toEqual(payload)
    expect(previa.skipNewCategories ?? []).toEqual([])
    expect(confirm.skipNewCategories).toEqual(['qa ia grupo novo > qa ia desmarcada'])

    // --- o resultado nas OUTRAS telas ------------------------------------------
    await page.goto('/categorias')
    const despesas = page.getByRole('region', { name: 'Despesas' })
    await expect(despesas.getByText(GRUPO_NOVO, { exact: true })).toBeVisible()
    await expect(despesas.getByText(FOLHA_CRIADA, { exact: true })).toBeVisible()
    await expect(page.getByText(FOLHA_DESMARCADA, { exact: true })).toHaveCount(0)

    // A criada tem a palavra dela; o grupo novo NÃO tem campo de palavra
    // (tem filha); o grupo alvo recebeu a palavra de categoria, uma vez.
    expect(await palavrasNoDialogoDaCategoria(page, FOLHA_CRIADA)).toEqual(['zumbra ia'])
    expect(await palavrasNoDialogoDaCategoria(page, GRUPO_ALVO)).toEqual(['plintaq ia'])
    await page.getByRole('button', { name: `Editar ${GRUPO_NOVO}`, exact: true }).click()
    const doGrupoNovo = page.locator('dialog[open]')
    await expect(campoDePalavras(doGrupoNovo)).toHaveCount(0)
    await expect(doGrupoNovo.getByText('Palavras-chave ficam nas subcategorias.')).toBeVisible()
    await page.keyboard.press('Escape')

    // A palavra de conta aparece no diálogo da conta.
    expect(await palavrasNoDialogoDaConta(page, CONTA_ALVO)).toEqual(['vrandix ia'])

    // E a desmarcada não deixou palavra em lugar nenhum: as palavras dela
    // não estão em NENHUMA categoria da casa.
    const arvoreFinal = (await (await page.request.get('/api/v1/categories')).json()) as ArvoreJSON
    expect(categoriaNaArvore(arvoreFinal, FOLHA_DESMARCADA)).toBeUndefined()
    const todas = JSON.stringify(arvoreFinal)
    expect(todas).not.toContain('krevol ia')
  })

  /** Reimportar o MESMO JSON, agora com tudo marcado: a criada vira
   *  `merged_into_existing` com a palavra já presente, a desmarcada da vez
   *  anterior nasce agora, e a prévia diz exatamente isso. Aceite 12 pela
   *  tela. */
  test('reimportar o mesmo JSON: o que já entrou é pulado, e só o que faltava entra', async ({
    page,
  }) => {
    const arvore = (await (await page.request.get('/api/v1/categories')).json()) as ArvoreJSON
    const loja = categoriaNaArvore(arvore, GRUPO_ALVO)
    const contas = (await (await page.request.get('/api/v1/accounts')).json()) as {
      items: Array<{ id: string; name: string }>
    }
    const conta = contas.items.find((c) => c.name === CONTA_ALVO)

    const payload = {
      homefinanceKeywordImport: 1,
      newCategories: [
        { group: GRUPO_NOVO, name: FOLHA_CRIADA, kind: 'expense', add: ['zumbra ia'] },
        { group: GRUPO_NOVO, name: FOLHA_DESMARCADA, add: ['krevol ia', 'krevol ia dois'] },
      ],
      categoryKeywords: [{ categoryId: loja?.id, categoryPath: GRUPO_ALVO, add: ['plintaq ia'] }],
      accountKeywords: [{ accountId: conta?.id, accountName: CONTA_ALVO, add: ['vrandix ia'] }],
    }

    await page.goto('/ia')
    const importar = page.getByRole('region', { name: '2 · Importar o que a IA respondeu' })
    await importar
      .getByLabel('Cole aqui o JSON que a IA respondeu')
      .fill(JSON.stringify(payload, null, 2))
    await importar.getByRole('button', { name: 'Conferir', exact: true }).click()

    // Só a que faltava é "a criar"; as 3 palavras que já estavam lá são puladas.
    await expect(page.getByRole('heading', { name: 'Categorias a criar · 1' })).toBeVisible()
    await expect(
      page.getByRole('checkbox', {
        name: `Criar ${GRUPO_NOVO} > ${FOLHA_DESMARCADA} com 2 palavras-chave`,
      }),
    ).toBeChecked()
    await expect(page.getByRole('status').filter({ hasText: /palavras entram/ })).toHaveText(
      '1 categoria nova · 2 palavras entram · 3 já estavam lá.',
    )
    await expect(page.getByText('Ver o que não entra · 3')).toBeVisible()

    await page.getByRole('button', { name: 'Criar 1 categoria e gravar 2 palavras' }).click()
    await expect(page.getByRole('status').filter({ hasText: /gravadas\./ })).toHaveText(
      '1 categoria criada e 2 palavras gravadas.',
    )

    expect(await palavrasNoDialogoDaCategoria(page, FOLHA_DESMARCADA)).toEqual([
      'krevol ia',
      'krevol ia dois',
    ])
    // A que já existia não ganhou uma segunda cópia da palavra.
    expect(await palavrasNoDialogoDaCategoria(page, FOLHA_CRIADA)).toEqual(['zumbra ia'])
  })

  /** As recusas de forma pela pilha inteira (roteador, `requireAuth`,
   *  limitador por casa, `MaxBytesReader`, handler). O 413 só existe de
   *  verdade aqui: em `httptest` o middleware é montado à mão. */
  test('a API recusa forma errada com 400, corpo grande com 413, e nunca ecoa o colado', async ({
    page,
  }) => {
    const casos: Array<{ nome: string; corpo: unknown; status: number; campo?: string }> = [
      {
        nome: 'versão como texto',
        corpo: {
          payload: { homefinanceKeywordImport: '1', categoryKeywords: [] },
          fromMonth: '2026-07',
          toMonth: '2026-09',
        },
        status: 400,
        campo: 'payload.homefinanceKeywordImport',
      },
      {
        nome: 'campo desconhecido numa entrada (caixa trocada)',
        corpo: {
          payload: {
            homefinanceKeywordImport: 1,
            categoryKeywords: [{ CategoryId: 'x', categoryPath: 'x', add: ['ECO-DO-COLADO'] }],
          },
          fromMonth: '2026-07',
          toMonth: '2026-09',
        },
        status: 400,
      },
      {
        nome: 'campo capaz de excluir',
        corpo: {
          payload: { homefinanceKeywordImport: 1, deleteCategories: ['ECO-DO-COLADO'] },
          fromMonth: '2026-07',
          toMonth: '2026-09',
        },
        status: 400,
      },
      {
        nome: 'listas vazias',
        corpo: {
          payload: { homefinanceKeywordImport: 1 },
          fromMonth: '2026-07',
          toMonth: '2026-09',
        },
        status: 400,
        campo: 'payload',
      },
      {
        nome: 'janela de 4 meses',
        corpo: {
          payload: { homefinanceKeywordImport: 1, accountKeywords: [] },
          fromMonth: '2026-06',
          toMonth: '2026-09',
        },
        status: 400,
        campo: 'toMonth',
      },
      {
        nome: 'householdId no envelope',
        corpo: {
          payload: { homefinanceKeywordImport: 1, accountKeywords: [] },
          fromMonth: '2026-07',
          toMonth: '2026-09',
          householdId: 'ECO-DO-COLADO',
        },
        status: 400,
      },
    ]
    for (const rota of ['preview', 'confirm']) {
      for (const caso of casos) {
        const resposta = await page.request.post(`/api/v1/ai/keyword-import/${rota}`, {
          data: caso.corpo,
        })
        expect(resposta.status(), `${rota}: ${caso.nome}`).toBe(caso.status)
        const corpo = await resposta.json()
        expect(corpo.error.code, `${rota}: ${caso.nome}`).toBe('VALIDATION_FAILED')
        if (caso.campo) {
          expect(Object.keys(corpo.error.fields ?? {}), `${rota}: ${caso.nome}`).toEqual([caso.campo])
        }
        expect(await resposta.text()).not.toContain('ECO-DO-COLADO')
      }

      // 200 KB → 413 antes de qualquer parsing de negócio (aceite 20). Direto
      // na API (o cookie de sessão é por host, não por porta): o Go responde
      // 413 e FECHA a conexão sem ler o resto do corpo, e um proxy no meio
      // pode ver isso como ECONNRESET — MEDIDO em 21/09/2026: pelo proxy do
      // Vite a mesma requisição volta **502**. O que o backend garante é o
      // 413; o que chega à tela através de um proxy é achado do QA, não
      // regra desta suíte.
      const corpoGrande = JSON.stringify({
        payload: { homefinanceKeywordImport: 1, notes: 'n'.repeat(200 * 1024) },
        fromMonth: '2026-07',
        toMonth: '2026-09',
      })
      const grande = await page.request.post(`${API_URL}/api/v1/ai/keyword-import/${rota}`, {
        headers: { 'Content-Type': 'application/json' },
        data: corpoGrande,
      })
      expect(grande.status(), `${rota}: 200 KB direto na API`).toBe(413)
      expect((await grande.json()).error.code).toBe('PAYLOAD_TOO_LARGE')
      const peloProxy = await page.request.post(`/api/v1/ai/keyword-import/${rota}`, {
        headers: { 'Content-Type': 'application/json' },
        data: corpoGrande,
        failOnStatusCode: false,
      })
      expect([413, 502], `${rota}: 200 KB pelo proxy (413 ideal; 502 = conexão fechada cedo)`).toContain(
        peloProxy.status(),
      )
    }
  })

  /** BOLA pela pilha real: o `categoryId` de uma categoria que esta casa não
   *  tem — aqui, um id inventado com forma canônica, que é indistinguível de
   *  um id de outra casa para quem só tem o token desta — volta
   *  `item_not_found` com `name` nulo, mesmo com o caminho "certo". */
  test('id que não é desta casa é item_not_found, sem nome, nas duas rotas', async ({ page }) => {
    for (const rota of ['preview', 'confirm']) {
      const resposta = await page.request.post(`/api/v1/ai/keyword-import/${rota}`, {
        data: {
          payload: {
            homefinanceKeywordImport: 1,
            categoryKeywords: [
              {
                categoryId: '018f0000-0000-7000-8000-0000000c0ffe',
                categoryPath: 'Alimentação > Mercado',
                add: ['zumbra alheio'],
              },
            ],
            accountKeywords: [
              {
                accountId: '018f0000-0000-7000-8000-0000000c0ffe',
                accountName: CONTA_ALVO,
                add: ['vrandix alheio'],
              },
            ],
          },
          fromMonth: '2026-07',
          toMonth: '2026-09',
        },
      })
      expect(resposta.status(), rota).toBe(200)
      const relatorio = await resposta.json()
      expect(relatorio.items).toHaveLength(2)
      for (const item of relatorio.items) {
        expect(item.name, rota).toBeNull()
        expect(item.added, rota).toEqual([])
        expect(item.rejected.map((r: { reason: string }) => r.reason), rota).toEqual([
          'item_not_found',
        ])
      }
      expect(relatorio.totals.added).toBe(0)
    }
  })
})
