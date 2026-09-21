import { expect, type Page, test } from '@playwright/test'
import { API_URL } from './support/ambiente'

/** Ponta a ponta da importação (E2 / spec 0004 §10.15), contra a API Go real.
 *
 *  O que só este nível prova, e que nenhum teste de componente alcança:
 *
 *  - o **multipart sai do navegador de verdade**. O `client.ts` precisa deixar
 *    o `Content-Type` para o navegador definir, porque é ele quem sabe o
 *    `boundary`. Num teste com `fetch` mockado isso passa de qualquer jeito;
 *    aqui, se alguém "arrumar" o cliente pondo `application/json` de volta, o
 *    upload responde 400 e este teste fica vermelho na hora;
 *  - o fluxo atravessa **três rotas** (`/importar` → `/importar/{id}/revisar` →
 *    `/importar/{id}/resultado`) num roteador real, carregando um id de lote
 *    que veio do servidor;
 *  - a **dedução de duplicata acontece no servidor**: a segunda importação do
 *    mesmo arquivo volta com tudo marcado, e o botão de confirmar muda de texto
 *    sozinho;
 *  - o **CSRF** em multipart. Multipart/form-data é *simple request*: não tem
 *    preflight, e é exatamente por isso que ele é o caminho clássico de CSRF.
 *    A defesa é `SameSite=Strict` mais a checagem de `Origin`/`Sec-Fetch-Site`,
 *    e só um navegador de verdade produz os cabeçalhos que a exercitam.
 *
 *  **Compartilha a casa** criada pelo projeto `setup` (ver `e2e/sessao.setup.ts`
 *  para o porquê), roda em modo serial e usa nomes próprios para não colidir
 *  com o que as outras specs criaram. */

test.describe.configure({ mode: 'serial' })

const CONTA = 'Conta da Importação'
const MES = '2026-08'

/** O extrato de teste, montado aqui em vez de lido de um arquivo.
 *
 *  Gerar o conteúdo no próprio teste evita acoplar o frontend ao caminho das
 *  fixtures do backend — e deixa à vista, para quem lê, exatamente qual dado
 *  entra e qual resultado ele deve produzir. O formato é o do extrato Nubank
 *  (`Data,Valor,Identificador,Descrição`, `DD/MM/YYYY`, negativo é saída).
 *
 *  **Os nomes são inventados de propósito** (ADR-033, 18/09/2026). Eram
 *  `Mercado`, `Farmacia`, `Salario` e `Posto`; com a semente de categorias,
 *  casa nova nasce reconhecendo as quatro palavras e as quatro linhas entrariam
 *  JÁ categorizadas. Isso quebraria `relatorio-por-categoria.spec.ts`, que lê
 *  este mês e precisa de uma lacuna "Sem categoria" para categorizar à mão.
 *  `Dornek`, `Vrandix`, `Tarvin` e `Mulfaz` foram conferidos contra o motor
 *  real (`internal/textmatch` + `internal/category/seed.go`): nenhum alcança o
 *  limiar de 80 em nenhum dos dois lados do dinheiro. */
const LINHAS = [
  { dia: '03', valor: '-50.00', id: '01', descricao: 'Dornek Exemplo' },
  { dia: '07', valor: '-19.90', id: '02', descricao: 'Vrandix Exemplo' },
  { dia: '12', valor: '1200.00', id: '03', descricao: 'Tarvin Exemplo' },
  { dia: '20', valor: '-33.00', id: '04', descricao: 'Mulfaz Exemplo' },
]

function extratoCSV(): Buffer {
  const cabecalho = 'Data,Valor,Identificador,Descrição\n'
  const corpo = LINHAS.map(
    (l) => `${l.dia}/08/2026,${l.valor},11111111-1111-4111-8111-1111111111${l.id},${l.descricao}\n`,
  ).join('')
  return Buffer.from(cabecalho + corpo, 'utf-8')
}

/** Envia o arquivo pelo passo 1 e devolve a página já na revisão. */
async function enviarExtrato(page: Page): Promise<void> {
  await page.goto('/importar')
  await expect(page.getByRole('heading', { level: 1, name: 'Importar extrato ou fatura' })).toBeVisible()

  await page.getByLabel('Conta de destino').selectOption({ label: CONTA })
  await page.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
    name: 'NU_2026-08.csv',
    mimeType: 'text/csv',
    buffer: extratoCSV(),
  })

  await page.getByRole('button', { name: 'Analisar arquivo' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' })).toBeVisible()
  await expect(page).toHaveURL(/\/importar\/[0-9a-f-]+\/revisar$/)
}

test.describe('importação', () => {
  test('a conta de destino existe antes do resto', async ({ page }) => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta', exact: true }).click()

    await page.getByLabel('Nome').fill(CONTA)
    await page.getByLabel('Tipo').selectOption('checking')
    await page.getByRole('button', { name: 'Criar conta' }).click()

    await expect(page.getByRole('row', { name: new RegExp(CONTA) })).toBeVisible()
  })

  test('importar CSV, revisar, confirmar, ver na lista e excluir', async ({ page }) => {
    // --- passo 1: enviar ---------------------------------------------------
    await enviarExtrato(page)

    // --- passo 2: revisar --------------------------------------------------
    // O resumo vem do SERVIDOR: 4 linhas lidas, nenhuma colisão.
    await expect(page.getByRole('status').filter({ hasText: '4 linhas lidas' })).toBeVisible()
    await expect(page.getByRole('heading', { level: 2, name: 'Prontas para importar' })).toBeVisible()

    for (const linha of LINHAS) {
      await expect(page.getByRole('row', { name: new RegExp(linha.descricao) })).toBeVisible()
    }

    // O botão diz o que vai acontecer, e o número é o do servidor.
    const confirmar = page.getByRole('button', { name: 'Importar 4 lançamentos' })
    await expect(confirmar).toBeVisible()
    await confirmar.click()

    // --- passo 3: resultado ------------------------------------------------
    await expect(page.getByRole('heading', { level: 1, name: 'Importação concluída' })).toBeVisible()
    await expect(page).toHaveURL(/\/importar\/[0-9a-f-]+\/resultado$/)
    await expect(page.getByRole('status').filter({ hasText: '4 lançamentos importados' })).toBeVisible()

    // --- a lista -----------------------------------------------------------
    await page.getByRole('button', { name: 'Ver os lançamentos de agosto' }).click()
    await expect(page).toHaveURL(new RegExp(`/lancamentos\\?mes=${MES}`))
    await expect(page.getByRole('heading', { level: 1, name: 'Lançamentos' })).toBeVisible()

    for (const linha of LINHAS) {
      await expect(page.getByRole('row', { name: new RegExp(linha.descricao) })).toBeVisible()
    }

    // Os sinais chegaram certos: negativo é saída, positivo é entrada.
    //
    // A asserção é sobre o texto para LEITOR DE TELA ("negativos"/"positivos"),
    // e não sobre o visível: o visível é `-50,00` com `aria-hidden`, e casá-lo
    // por substring deixaria passar uma tela que comunica o sinal só por cor —
    // que é justamente o que o projeto proíbe.
    await expect(
      page.getByRole('row', { name: /Dornek Exemplo/ }).getByText('R$ 50,00 negativos', { exact: true }),
    ).toBeAttached()
    await expect(
      page.getByRole('row', { name: /Tarvin Exemplo/ }).getByText('R$ 1.200,00 positivos', { exact: true }),
    ).toBeAttached()
    // E o visível traz o sinal escrito, não só a cor.
    await expect(page.getByRole('row', { name: /Tarvin Exemplo/ }).getByText('+1.200,00')).toBeVisible()

    // --- excluir -----------------------------------------------------------
    await page.getByRole('button', { name: /^Excluir Mulfaz Exemplo,/ }).click()
    await expect(page.getByRole('heading', { level: 2, name: 'Excluir este lançamento?' })).toBeVisible()
    await page.getByRole('button', { name: 'Excluir lançamento' }).click()

    await expect(page.getByRole('row', { name: /Mulfaz Exemplo/ })).toHaveCount(0)
    // E os outros três continuam lá: a exclusão é de UM lançamento.
    await expect(page.getByRole('row', { name: /Dornek Exemplo/ })).toBeVisible()
  })

  test('reimportar o mesmo arquivo não deixa nada entrar', async ({ page }) => {
    await enviarExtrato(page)

    // O aviso "este mesmo conteúdo já foi importado" (§4.1) chega até aqui: a
    // tela de revisão carrega o lote por `GET /imports/{id}`, e esse caminho
    // devolve `sameContentImportedAt` preenchido quando outra importação
    // CONFIRMADA da casa já gravou o mesmo conteúdo — que é o caso, porque o
    // teste anterior confirmou este arquivo.
    //
    // Até 17/09/2026 o campo só existia na resposta 201 de `POST /imports`, e
    // esta asserção era `toHaveCount(0)` para não deixar o defeito esquecido.
    // É AVISO, nunca bloqueio: o botão de confirmar segue disponível.
    await expect(
      page.getByRole('alert').filter({ hasText: 'Este mesmo conteúdo já foi importado' }),
    ).toBeVisible()

    // As linhas que continuam vivas ficam no bloco "ficam de fora"; a que foi
    // excluída no teste anterior volta como "Já importada e excluída", que é
    // liberável e restaura o original.
    await expect(page.getByRole('rowheader', { name: /Já importada e excluída · 1/ })).toBeVisible()
    await expect(page.getByText(/Ficam de fora · 3 linhas/)).toBeVisible()

    // Com os padrões, NADA entra — e o botão diz isso.
    const confirmar = page.getByRole('button', { name: 'Nada marcado para importar' })
    await expect(confirmar).toBeVisible()
    await expect(confirmar).toHaveAttribute('aria-disabled', 'true')

    // E sai pelo cancelamento, sem gravar nada.
    await page.getByRole('button', { name: 'Cancelar importação' }).click()
    await expect(page.getByRole('heading', { level: 2, name: 'Cancelar esta importação?' })).toBeVisible()
    await page.getByRole('button', { name: 'Descartar o arquivo' }).click()

    await page.goto(`/lancamentos?mes=${MES}`)
    // Continuam sendo as MESMAS três linhas: nenhuma duplicata nasceu.
    await expect(page.getByRole('row', { name: /Dornek Exemplo/ })).toHaveCount(1)
    await expect(page.getByRole('row', { name: /Vrandix Exemplo/ })).toHaveCount(1)
    await expect(page.getByRole('row', { name: /Tarvin Exemplo/ })).toHaveCount(1)
  })
})

test.describe('CSRF em multipart', () => {
  /** POST multipart de OUTRA origem tem de levar 403 (spec 0004 §6.11).
   *
   *  Por que precisa ser navegador: `multipart/form-data` é *simple request* —
   *  não dispara preflight —, então a única coisa entre um site hostil e o
   *  `POST /imports` são os cabeçalhos que o navegador anexa sozinho
   *  (`Origin`, `Sec-Fetch-Site`) e o `SameSite=Strict` do cookie. Um cliente
   *  HTTP de teste não produz nenhum dos três, e "passou" com ele não significa
   *  nada.
   *
   *  O ataque é reproduzido com um `<form>` de verdade, e não com `fetch`, por
   *  dois motivos. Primeiro porque é literalmente o vetor: `<form
   *  enctype="multipart/form-data">` apontando para a API é o CSRF clássico, e
   *  ele funciona sem uma linha de JavaScript na página hostil. Segundo porque
   *  o `fetch` cross-origin sem CORS é abortado pelo navegador de um jeito que
   *  às vezes nem chega a virar requisição observável — e um teste que às vezes
   *  não envia o pedido não prova nada sobre o servidor.
   *
   *  A asserção é sobre a resposta de REDE que o Playwright observa: a página
   *  hostil, por construção, não lê nada. */
  test('POST multipart de outra origem é barrado com 403', async ({ page }) => {
    // A origem hostil é `localhost` (o app é servido em `127.0.0.1`): são
    // origens DIFERENTES para o navegador — é o que faz o pedido ser
    // cross-site — e as duas estão no mesmo espaço de endereços local, o que
    // evita o bloqueio de Private Network Access do Chrome. Com um domínio
    // público inventado, o navegador recusa o pedido ANTES de enviá-lo, e o
    // teste passaria a medir a proteção do navegador em vez da do servidor.
    const ORIGEM_HOSTIL = 'http://localhost:5199'

    await page.route(`${ORIGEM_HOSTIL}/**`, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'text/html; charset=utf-8',
        body: '<!doctype html><title>Pagina hostil</title><body>ok</body>',
      }),
    )
    await page.goto(`${ORIGEM_HOSTIL}/ataque.html`)

    const [observada] = await Promise.all([
      page.waitForResponse(
        (r) => r.url() === `${API_URL}/api/v1/imports` && r.request().method() === 'POST',
      ),
      // O `catch` é esperado: o `submit()` navega a página, e o contexto de
      // execução morre embaixo do `evaluate`.
      page
        .evaluate((api) => {
          const form = document.createElement('form')
          form.method = 'POST'
          form.action = `${api}/api/v1/imports`
          form.enctype = 'multipart/form-data'

          const campo = document.createElement('input')
          campo.type = 'hidden'
          campo.name = 'accountId'
          campo.value = '00000000-0000-7000-8000-000000000001'
          form.appendChild(campo)

          document.body.appendChild(form)
          form.submit()
        }, API_URL)
        .catch(() => undefined),
    ])

    expect(observada.status(), 'o servidor recusa o multipart de outra origem').toBe(403)

    // `allHeaders()` e não `headers()`: os cabeçalhos que o NAVEGADOR
    // acrescenta sozinho — que são justamente os que sustentam esta defesa —
    // só aparecem na versão completa.
    const cabecalhos = await observada.request().allHeaders()
    expect(cabecalhos['origin'], 'o navegador declara a origem hostil').toBe(ORIGEM_HOSTIL)
    // ⚠️ Achado do próprio teste, e a razão de o `CSRFGuard` ter DUAS regras:
    // nesta navegação por formulário o Chrome NÃO enviou `Sec-Fetch-Site`.
    // Quem recusou o pedido foi o ramo de baixo do guarda — "sem
    // Sec-Fetch-Site e com Origin: exige allowlist". Se a defesa dependesse só
    // do `Sec-Fetch-Site`, este ataque teria passado.
    //
    // A asserção é, portanto, sobre o sinal que EXISTE: quando o cabeçalho
    // vier, ele tem de dizer `cross-site`; quando não vier, o `Origin` tem de
    // ser o da origem hostil — e ele é, conferido logo acima.
    if (cabecalhos['sec-fetch-site'] !== undefined) {
      expect(cabecalhos['sec-fetch-site']).toBe('cross-site')
    }
    expect(
      cabecalhos['content-type'],
      'é multipart mesmo: o caminho que não tem preflight',
    ).toContain('multipart/form-data')

    // A PRIMEIRA camada de defesa, provada de graça: com `SameSite=Strict` o
    // navegador nem manda a credencial num pedido cross-site. Mesmo que o
    // CSRFGuard não existisse, não haveria sessão para roubar.
    expect(cabecalhos['cookie'], 'SameSite=Strict: nenhum cookie viaja').toBeUndefined()

    // E o corpo do 403 é o genérico do projeto: nada de detalhe que ajude a
    // calibrar o ataque.
    const corpo = await observada.text()
    expect(corpo).toContain('FORBIDDEN')
    expect(corpo).not.toContain('Origin')
    expect(corpo).not.toContain('CSRF')
  })
})
