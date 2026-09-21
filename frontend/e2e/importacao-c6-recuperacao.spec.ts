import { expect, type Page, test } from '@playwright/test'

/** O beco sem saída que virou caminho, ponta a ponta contra a API Go real
 *  (E2 / spec 0004 §3.3, ponto 2). Este é o fluxo que o usuário reclamou:
 *
 *    criar conta corrente C6 → tentar importar a FATURA do C6 nela →
 *    ver o bloco de recuperação → criar "Cartão C6" ali → reimportar a fatura
 *    na conta de cartão → confirmar → ver os lançamentos.
 *
 *  Por que SÓ o E2E prova isto (nenhum teste de componente alcança):
 *
 *  - o **backend real** precisa detectar o C6 na fatura e emitir o 422
 *    `IMPORT_TARGET_MISMATCH` com `fields.reason = expected_credit_card` e
 *    `fields.detectedInstitution = c6`. Um `fetch` mockado inventa esses campos;
 *    aqui eles vêm do parser do C6 registrado em `cmd/api/main.go`;
 *  - o **frontend reage** aos `fields`: mostra o bloco de recuperação em pt-BR,
 *    já nomeando o C6, e abre o `AccountDialog` (agora em `components/`)
 *    pré-preenchido como cartão de crédito do C6;
 *  - o **arquivo selecionado sobrevive** ao erro de conta — a pessoa não
 *    re-seleciona nada, só aponta o destino certo e reenvia;
 *  - a segunda tentativa, agora na conta de cartão, **atravessa as três rotas**
 *    (`/importar` → `/revisar` → `/resultado`) e grava os lançamentos, que
 *    aparecem na lista pela competência da fatura.
 *
 *  Compartilha a casa do projeto `setup` e usa nomes próprios para não colidir
 *  com o que as outras specs criaram. */

test.describe.configure({ mode: 'serial' })

const CONTA_CORRENTE_C6 = 'C6 Conta Corrente'
const CARTAO_C6 = 'Cartão C6'

/** A fatura do C6, montada aqui em vez de lida de arquivo — deixa à vista qual
 *  dado entra. O formato é o `c6.card_statement.v1`: separador `;`, 9 colunas,
 *  ponto decimal, POSITIVO é compra (saída). Sem preâmbulo.
 *
 *  **As descrições são inventadas** (ADR-033, 18/09/2026). A primeira chamava-se
 *  `MERCADO QA EXEMPLO` e, com a semente de categorias, passou a casar
 *  «minimercado» por aproximação (89): a linha entrava categorizada em
 *  "Alimentação › Supermercado" sem que nenhum teste daqui tivesse pedido isso.
 *  Não quebrava nada — este arquivo não afirma categoria —, mas era um
 *  acoplamento silencioso esperando a primeira asserção de categoria. `QUIRPEL`,
 *  `MOVEL` e `APP` foram conferidos contra o motor real (`internal/textmatch`
 *  com a lista de `internal/category/seed.go`): os três dão 0 nos dois lados do
 *  dinheiro, bem abaixo do limiar de 80. */
const COMPRAS = [
  { data: '03/09/2026', descricao: 'QUIRPEL QA EXEMPLO', parcela: 'Única', valor: '120.00' },
  { data: '05/09/2026', descricao: 'MOVEL QA EXEMPLO', parcela: '2/7', valor: '284.01' },
  { data: '10/09/2026', descricao: 'APP QA EXEMPLO', parcela: 'Única', valor: '9.57' },
]

function faturaC6CSV(): Buffer {
  const cabecalho =
    'Data de Compra;Nome no Cartão;Final do Cartão;Categoria;Descrição;Parcela;Valor (em US$);Cotação (em R$);Valor (em R$)\n'
  const corpo = COMPRAS.map(
    (c) => `${c.data};FULANO EXEMPLO;1111;Casa;${c.descricao};${c.parcela};0;0;${c.valor}\n`,
  ).join('')
  return Buffer.from(cabecalho + corpo, 'utf-8')
}

/** Seleciona a conta de destino, escolhe a fatura e pede análise. */
async function tentarImportarFatura(page: Page, contaLabel: string): Promise<void> {
  await page.getByLabel('Conta de destino').selectOption({ label: contaLabel })
  await page.getByLabel('Arquivo do extrato ou da fatura').setInputFiles({
    name: 'Fatura_C6.csv',
    mimeType: 'text/csv',
    buffer: faturaC6CSV(),
  })
  await page.getByRole('button', { name: 'Analisar arquivo' }).click()
}

test.describe('importação C6 — o beco sem saída virou caminho', () => {
  test('a conta corrente C6 existe antes do resto', async ({ page }) => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta', exact: true }).click()

    await page.getByLabel('Nome').fill(CONTA_CORRENTE_C6)
    await page.getByLabel('Tipo').selectOption('checking')
    await page.getByLabel('Instituição').selectOption('c6')
    await page.getByRole('button', { name: 'Criar conta' }).click()

    await expect(page.getByRole('row', { name: new RegExp(CONTA_CORRENTE_C6) })).toBeVisible()
  })

  test('fatura na conta corrente vira caminho: criar o cartão, reenviar, confirmar, ver', async ({
    page,
  }) => {
    // --- passo 1: a fatura na conta ERRADA ---------------------------------
    await page.goto('/importar')
    await expect(
      page.getByRole('heading', { level: 1, name: 'Importar extrato ou fatura' }),
    ).toBeVisible()

    await tentarImportarFatura(page, CONTA_CORRENTE_C6)

    // O backend real detectou a FATURA do C6 e recusou a conta corrente — mas
    // com um caminho, não um beco. O bloco de recuperação nomeia o C6.
    const aviso = page.getByRole('alert').filter({ hasText: 'Isto parece uma fatura de cartão.' })
    await expect(aviso).toBeVisible()
    await expect(
      aviso.getByText(/Faturas vão para uma conta de cartão de crédito/),
    ).toBeVisible()

    // O arquivo escolhido SOBREVIVE ao erro de conta: a pessoa não re-seleciona.
    await expect(page.getByText('Fatura_C6.csv')).toBeVisible()

    // --- passo 2: criar "Cartão C6" ali mesmo, já preenchido ---------------
    await page.getByRole('button', { name: 'Criar conta de cartão do C6' }).click()

    // O AccountDialog (movido para components/) abre pré-preenchido pela
    // instituição detectada: tipo cartão, instituição C6, nome sugerido.
    await expect(page.getByRole('heading', { name: 'Nova conta' })).toBeVisible()
    await expect(page.getByLabel('Nome')).toHaveValue(CARTAO_C6)
    await expect(page.getByLabel('Tipo')).toHaveValue('credit_card')
    await expect(page.getByLabel('Instituição')).toHaveValue('c6')

    // `exact` porque o bloco de recuperação ainda mostra "Criar conta de cartão
    // do C6"; queremos o submit do diálogo.
    await page.getByRole('button', { name: 'Criar conta', exact: true }).click()

    // A conta nova entra no seletor e já fica pré-selecionada como destino
    // (algo passou a estar selecionado). A prova de que é o CARTÃO vem no passo
    // seguinte: reanalisar dá certo em vez de repetir o mismatch — só um
    // credit_card faz a fatura passar.
    await expect(page.getByLabel('Conta de destino')).toHaveValue(/.+/)

    // --- passo 3: reenviar a MESMA fatura, agora no cartão -----------------
    // O arquivo continua lá; basta reanalisar.
    await page.getByRole('button', { name: 'Analisar arquivo' }).click()

    await expect(
      page.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
    ).toBeVisible()
    await expect(page).toHaveURL(/\/importar\/[0-9a-f-]+\/revisar$/)

    // As três compras aparecem prontas para importar.
    await expect(
      page.getByRole('heading', { level: 2, name: 'Prontas para importar' }),
    ).toBeVisible()
    for (const compra of COMPRAS) {
      await expect(page.getByRole('row', { name: new RegExp(compra.descricao) })).toBeVisible()
    }

    // A fatura pediu competência/fechamento/vencimento, já sugeridos pelo servidor.
    await expect(page.getByLabel('Competência')).toBeVisible()

    // --- passo 4: confirmar -------------------------------------------------
    const confirmar = page.getByRole('button', { name: /Importar \d+ lançament/ })
    await expect(confirmar).toBeVisible()
    await confirmar.click()

    await expect(
      page.getByRole('heading', { level: 1, name: 'Importação concluída' }),
    ).toBeVisible()
    await expect(page).toHaveURL(/\/importar\/[0-9a-f-]+\/resultado$/)

    // --- passo 5: ver os lançamentos ---------------------------------------
    await page.getByRole('button', { name: /Ver os lançamentos de/ }).click()
    await expect(page).toHaveURL(/\/lancamentos\?mes=2026-09/)
    await expect(page.getByRole('heading', { level: 1, name: 'Lançamentos' })).toBeVisible()

    for (const compra of COMPRAS) {
      await expect(page.getByRole('row', { name: new RegExp(compra.descricao) })).toBeVisible()
    }

    // O SINAL chegou certo: compra positiva é SAÍDA. Se o parser do C6
    // invertesse o sinal, aqui a compra apareceria como entrada — o texto para
    // leitor de tela é "negativos" (saída), não "positivos".
    await expect(
      page
        .getByRole('row', { name: /QUIRPEL QA EXEMPLO/ })
        .getByText('R$ 120,00 negativos', { exact: true }),
    ).toBeAttached()
    // E a parcela foi preservada na descrição.
    await expect(page.getByRole('row', { name: /MOVEL QA EXEMPLO · 2\/7/ })).toBeVisible()
  })
})
