import { expect, test } from '@playwright/test'

/** Ponta a ponta das telas de contas e categorias (E1 / spec 0003), contra a
 *  API Go real.
 *
 *  O que só este nível prova: que o `<dialog>` e o `<select>` NATIVOS funcionam
 *  de verdade num navegador (no jsdom os dois são polyfill de teste), que a
 *  semente de categorias nasceu junto com a casa, e que o mês na URL sobrevive
 *  à troca de tela num roteador real.
 *
 *  **Estes testes compartilham UMA casa**, criada pelo projeto `setup` e
 *  herdada por `storageState` — o porquê está em `e2e/sessao.setup.ts`. Era
 *  imposição do teto de 5 cadastros/h por IP; desde 17/09/2026 é escolha, com
 *  a API de teste subindo em `RATE_LIMITS_PROFILE=test` (docs/SEGURANCA.md
 *  §5.2, recusado em produção).
 *
 *  Consequências que o arquivo assume, e que são explícitas de propósito:
 *  o modo é **serial** (a ordem importa e uma falha aborta o resto, em vez de
 *  produzir uma cascata de erros confusos), e cada teste usa **nomes próprios**
 *  para não colidir com o que os outros criaram. Só o primeiro teste de cada
 *  bloco pode contar com o estado inicial da casa. */

test.describe.configure({ mode: 'serial' })

test.describe('contas', () => {
  // PRIMEIRO teste do arquivo: é o único que pode contar com a casa vazia.
  test('casa nova não tem conta, e o vazio orienta o próximo passo', async ({ page }) => {
    await page.goto('/contas')
    await expect(page.getByRole('heading', { name: 'Contas' })).toBeVisible()

    await expect(page.getByText('Nenhuma conta ainda.')).toBeVisible()
    await expect(page.getByText(/Comece pela conta onde o dinheiro entra/)).toBeVisible()
  })

  test('criar, ver na lista com saldo, arquivar e desarquivar', async ({ page }) => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Criar a primeira conta' }).click()
    await expect(page.getByRole('heading', { name: 'Nova conta' })).toBeVisible()

    await page.getByLabel('Nome').fill('Conta Corrente')
    await page.getByLabel('Tipo').selectOption('checking')
    await page.getByLabel('Saldo de abertura').click()
    await page.keyboard.type('150000')
    await page.getByRole('button', { name: 'Criar conta' }).click()

    const linha = page.getByRole('row', { name: /Conta Corrente/ })
    await expect(linha).toBeVisible()
    // O valor visível da célula (a versão para leitor de tela traz "R$" e a
    // palavra "negativos", e é exercitada nos testes de componente).
    await expect(linha.getByText('1.500,00', { exact: true })).toBeVisible()

    // O total do rodapé bate com a única linha.
    await expect(
      page.getByRole('row', { name: /Total/ }).getByText('1.500,00', { exact: true }),
    ).toBeVisible()

    // Arquivar tira da lista padrão...
    await page.getByRole('button', { name: 'Arquivar Conta Corrente' }).click()
    await expect(page.getByRole('row', { name: /Conta Corrente/ })).toHaveCount(0)

    // ...e "mostrar arquivadas" traz de volta, com a etiqueta ESCRITA.
    //
    // `exact: true` e escopo na linha: o `getByText` do Playwright casa
    // SUBSTRING por padrão, então "Arquivada" pegaria também a célula e a linha
    // que a contêm, e o teste morreria por ambiguidade em vez de por defeito.
    await page.getByLabel('Mostrar arquivadas').check()
    const arquivada = page.getByRole('row', { name: /Conta Corrente/ })
    await expect(arquivada).toBeVisible()
    await expect(arquivada.getByText('Arquivada', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: 'Desarquivar Conta Corrente' }).click()
    await page.getByLabel('Mostrar arquivadas').uncheck()
    await expect(page.getByRole('row', { name: /Conta Corrente/ })).toBeVisible()
  })

  test('cartão com saldo negativo mostra o sinal', async ({ page }) => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta' }).click()

    await page.getByLabel('Nome').fill('Cartão Roxo')
    await page.getByLabel('Tipo').selectOption('credit_card')
    // O botão de sinal precisa funcionar ANTES de digitar — é assim que a
    // pessoa pensa ("é uma dívida, e vale tanto").
    await page.getByRole('button', { name: 'Valor negativo' }).click()
    await page.getByLabel('Saldo de abertura').click()
    await page.keyboard.type('80000')
    await page.getByRole('button', { name: 'Criar conta' }).click()

    const linha = page.getByRole('row', { name: /Cartão Roxo/ })
    await expect(linha).toBeVisible()
    // Sinal explícito, não só cor.
    await expect(linha.getByText('-800,00', { exact: true })).toBeVisible()
  })

  test('nome duplicado é recusado com o erro no campo certo', async ({ page }) => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta' }).click()
    await page.getByLabel('Nome').fill('Carteira')
    await page.getByRole('button', { name: 'Criar conta' }).click()
    await expect(page.getByRole('row', { name: /Carteira/ })).toBeVisible()

    await page.getByRole('button', { name: 'Nova conta' }).click()
    // Só a caixa muda: a colisão é por nome normalizado.
    await page.getByLabel('Nome').fill('carteira')
    await page.getByRole('button', { name: 'Criar conta' }).click()

    await expect(
      page.getByText('Já existe uma conta com este nome, ou o nome é inválido.'),
    ).toBeVisible()
    // O diálogo continua aberto para corrigir.
    await expect(page.getByRole('heading', { name: 'Nova conta' })).toBeVisible()
  })

  test('o diálogo fecha com Escape', async ({ page }) => {
    await page.goto('/contas')
    await page.getByRole('button', { name: 'Nova conta' }).click()
    await expect(page.getByRole('heading', { name: 'Nova conta' })).toBeVisible()

    // ESC vem do `<dialog>` nativo — uma das coisas que só o navegador de
    // verdade exercita (no jsdom ele é polyfill de teste).
    await page.keyboard.press('Escape')
    await expect(page.getByRole('heading', { name: 'Nova conta' })).toBeHidden()
  })
})

test.describe('categorias', () => {
  // D5 da spec 0003: a casa nasce com as categorias, na mesma transação em que
  // ela é criada. É aqui que isso se prova ponta a ponta.
  test('casa nova já vem com as categorias em português', async ({ page }) => {
    await page.goto('/categorias')
    await expect(page.getByRole('heading', { name: 'Categorias' })).toBeVisible()

    const despesas = page.getByRole('region', { name: 'Despesas' })
    const receitas = page.getByRole('region', { name: 'Receitas' })

    for (const grupo of ['Moradia', 'Alimentação', 'Transporte', 'Saúde']) {
      await expect(despesas.getByText(grupo, { exact: true })).toBeVisible()
    }
    await expect(receitas.getByText('Salário', { exact: true })).toBeVisible()
  })

  test('cria subcategoria que herda a natureza do grupo', async ({ page }) => {
    await page.goto('/categorias')
    await page.getByRole('button', { name: 'Nova subcategoria em Moradia' }).click()

    await expect(page.getByRole('heading', { name: 'Nova subcategoria' })).toBeVisible()
    // A tela não pergunta receita/despesa: ela EXPLICA a herança.
    await expect(page.getByLabel('Receita ou despesa')).toHaveCount(0)
    await expect(page.getByText(/Vai ficar dentro de/)).toBeVisible()

    await page.getByLabel('Nome').fill('Energia')
    await page.getByRole('button', { name: 'Criar' }).click()

    await expect(
      page.getByRole('region', { name: 'Despesas' }).getByText('Energia', { exact: true }),
    ).toBeVisible()
    // E continua sendo despesa: não vazou para o bloco de receitas.
    await expect(
      page.getByRole('region', { name: 'Receitas' }).getByText('Energia', { exact: true }),
    ).toHaveCount(0)
  })

  test('grupo com subcategoria não se exclui, e a tela oferece o caminho', async ({ page }) => {
    await page.goto('/categorias')
    // "Moradia" já ganhou "Energia" no teste anterior (modo serial).
    await page.getByRole('button', { name: 'Excluir Moradia' }).click()

    await expect(page.getByText('Não dá para excluir esta categoria')).toBeVisible()
    await expect(page.getByText(/arquive o grupo inteiro/)).toBeVisible()
    // O grupo continua lá.
    await expect(
      page.getByRole('region', { name: 'Despesas' }).getByText('Moradia', { exact: true }),
    ).toBeVisible()
  })

  test('arquivar o grupo leva as subcategorias junto', async ({ page }) => {
    await page.goto('/categorias')
    await page.getByRole('button', { name: 'Nova subcategoria em Lazer' }).click()
    await page.getByLabel('Nome').fill('Cinema')
    await page.getByRole('button', { name: 'Criar' }).click()
    await expect(page.getByText('Cinema', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: 'Arquivar Lazer' }).click()

    // Grupo invisível com filha visível é um estado que a tela não sabe
    // desenhar — por isso a cascata, e por isso ela é transacional.
    await expect(page.getByText('Lazer', { exact: true })).toHaveCount(0)
    await expect(page.getByText('Cinema', { exact: true })).toHaveCount(0)

    await page.getByLabel('Mostrar arquivadas').check()
    await expect(page.getByText('Lazer', { exact: true })).toBeVisible()
    await expect(page.getByText('Cinema', { exact: true })).toBeVisible()
  })
})

test.describe('casca do app', () => {
  test('o mês vive na URL e sobrevive à troca de tela', async ({ page }) => {
    await page.goto('/contas')
    await expect(page.getByRole('heading', { name: 'Contas' })).toBeVisible()

    await page.getByRole('button', { name: /Mês anterior/ }).click()

    const mes = new URL(page.url()).searchParams.get('mes')
    expect(mes).toMatch(/^\d{4}-\d{2}$/)

    // Trocar de tela mantém o mês — ele é o eixo do app.
    await page.getByRole('link', { name: 'Categorias' }).click()
    await expect(page.getByRole('heading', { name: 'Categorias' })).toBeVisible()
    expect(new URL(page.url()).searchParams.get('mes')).toBe(mes)

    // E um link colado abre exatamente o mesmo mês.
    await page.goto(`/contas?mes=${mes}`)
    await expect(page.getByRole('heading', { name: 'Contas' })).toBeVisible()
    expect(new URL(page.url()).searchParams.get('mes')).toBe(mes)
  })

  test('mês inválido na URL não quebra a tela', async ({ page }) => {
    await page.goto('/contas?mes=banana')

    await expect(page.getByRole('heading', { name: 'Contas' })).toBeVisible()
    // Caiu no mês corrente, e a tela continua utilizável.
    await expect(page.getByRole('button', { name: /Mês anterior/ })).toBeVisible()
  })

  test('a seção corrente é marcada para o leitor de tela', async ({ page }) => {
    await page.goto('/contas')
    await expect(page.getByRole('link', { name: 'Contas' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    await expect(page.getByRole('link', { name: 'Categorias' })).not.toHaveAttribute(
      'aria-current',
      'page',
    )
  })
})
