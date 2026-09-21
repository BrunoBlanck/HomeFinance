import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Account, CategoryTree, ImportRow } from '@/api/types'
import {
  CelulaDeCategoria,
  CelulaDescricaoDaLinha,
  type ContextoDaRevisao,
  rotuloDaLinha,
} from './CelulasDaRevisao'

/** Bordas das células compartilhadas da revisão que a tela inteira não
 *  exercita (QA, T13 da E2c).
 *
 *  A que mais importa: abaixo de 40rem a coluna Categoria some, e a sugestão
 *  **vai ser gravada** mesmo assim. A linha secundária "Categoria: Alimentação
 *  · sugerida" é o que torna esconder a coluna honesto (decisão (b) da seção
 *  E2c de `docs/DESIGN.md`). Ela é renderizada sempre e escondida por CSS, então
 *  aqui se prova o TEXTO, que é o que muda com a escolha da pessoa. */

const ALIMENTACAO = 'cat-alimentacao'
const TRANSPORTE = 'cat-transporte'
const CDB = 'cat-cdb'
const RESGATE = 'cat-resgate'

function linha(over: Partial<ImportRow> = {}): ImportRow {
  return {
    id: 'row-1',
    seq: 1,
    lineNo: 2,
    status: 'novo',
    defaultAction: 'import',
    allowedActions: ['import', 'skip'],
    kind: 'expense',
    occurredOn: '2026-07-03',
    amountCents: 5000,
    description: 'MERCADO DO SEU JOSE',
    externalId: null,
    rejectReason: null,
    matchTransactionId: null,
    suggestedCategoryId: null,
    matchScore: null,
    matchedKeyword: null,
    suggestedCounterpartAccountId: null,
    matchOccurredOn: null,
    ...over,
  }
}

const ARVORE: CategoryTree = {
  expense: [
    {
      id: ALIMENTACAO,
      name: 'Alimentação',
      kind: 'expense',
      parentId: null,
      keywords: ['supermercado'],
      archivedAt: null,
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
      children: [],
    },
    {
      id: TRANSPORTE,
      name: 'Transporte',
      kind: 'expense',
      parentId: null,
      keywords: [],
      archivedAt: null,
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
      children: [],
    },
  ],
  income: [],
  // As naturezas da E7 (ADR-029a): a de INVESTIMENTO cai do lado da despesa,
  // então uma linha de despesa tem de poder recebê-la; a de RESGATE está aqui
  // para provar o contrário — ela é do lado da receita e nunca aparece.
  investment: [
    {
      id: CDB,
      name: 'CDB',
      kind: 'investment',
      parentId: null,
      keywords: ['cdb'],
      archivedAt: null,
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
      children: [],
    },
  ],
  redemption: [
    {
      id: RESGATE,
      name: 'Resgate de CDB',
      kind: 'redemption',
      parentId: null,
      keywords: ['resgate cdb'],
      archivedAt: null,
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
      children: [],
    },
  ],
}

function contexto(escolhas: ContextoDaRevisao['escolhas'] = {}): ContextoDaRevisao {
  return {
    escolhas,
    contas: [] as Account[],
    contaDoLoteId: 'conta-lote',
    categorias: ARVORE,
    onEscolher: vi.fn(),
  }
}

describe('CelulaDescricaoDaLinha — linha secundária da categoria (celular)', () => {
  const sugerida = linha({
    suggestedCategoryId: ALIMENTACAO,
    matchScore: 88,
    matchedKeyword: 'supermercado',
  })

  it('com a sugestão intocada diz "Categoria: Alimentação · sugerida"', () => {
    render(<CelulaDescricaoDaLinha linha={sugerida} contexto={contexto()} />)
    expect(screen.getByText('Categoria: Alimentação · sugerida')).toBeInTheDocument()
  })

  it('com a categoria trocada à mão perde o "· sugerida"', () => {
    render(
      <CelulaDescricaoDaLinha
        linha={sugerida}
        contexto={contexto({ 'row-1': { acao: 'import', categoriaId: TRANSPORTE } })}
      />,
    )
    expect(screen.getByText('Categoria: Transporte')).toBeInTheDocument()
    expect(screen.queryByText(/sugerida/)).not.toBeInTheDocument()
  })

  it('com a sugestão LIMPA (categoryId: null) não escreve categoria nenhuma', () => {
    render(
      <CelulaDescricaoDaLinha
        linha={sugerida}
        contexto={contexto({ 'row-1': { acao: 'import', categoriaId: null } })}
      />,
    )
    expect(screen.queryByText(/^Categoria:/)).not.toBeInTheDocument()
  })

  it('escolher a PRÓPRIA sugerida de volta continua sendo "sugerida"', () => {
    render(
      <CelulaDescricaoDaLinha
        linha={sugerida}
        contexto={contexto({ 'row-1': { acao: 'import', categoriaId: ALIMENTACAO } })}
      />,
    )
    expect(screen.getByText('Categoria: Alimentação · sugerida')).toBeInTheDocument()
  })

  it('linha que não vai entrar como import não fala em categoria', () => {
    render(
      <CelulaDescricaoDaLinha
        linha={sugerida}
        contexto={contexto({ 'row-1': { acao: 'skip' } })}
      />,
    )
    expect(screen.queryByText(/^Categoria:/)).not.toBeInTheDocument()
  })

  it('a categoria de investimento é resolvida pelo NOME, como as outras naturezas', () => {
    render(
      <CelulaDescricaoDaLinha
        linha={linha({ suggestedCategoryId: CDB, matchScore: 92, matchedKeyword: 'cdb' })}
        contexto={contexto()}
      />,
    )
    expect(screen.getByText('Categoria: CDB · sugerida')).toBeInTheDocument()
  })

  it('linha nova sem sugestão e sem escolha fica muda — e anuncia "Sem pendência" a quem ouve', () => {
    render(<CelulaDescricaoDaLinha linha={linha()} contexto={contexto()} />)
    expect(screen.queryByText(/^Categoria:/)).not.toBeInTheDocument()
    expect(screen.getByText('Sem pendência.')).toHaveClass('sr-only')
  })
})

/** Critério de aceite 2 da spec 0006, no seletor da REVISÃO: sem ele a pessoa
 *  vê a sugestão de investimento na linha e não consegue escolhê-la à mão —
 *  que é exatamente o buraco que a troca por lado do dinheiro fecha. */
describe('CelulaDeCategoria — o seletor é do lado do dinheiro (ADR-029b)', () => {
  function opcoesDoSelect(): string[] {
    const select = screen.getByRole('combobox')
    return Array.from(select.querySelectorAll('option')).map((o) => o.value)
  }

  it('linha de DESPESA oferece despesa e investimento, e nunca resgate ou receita', () => {
    render(<CelulaDeCategoria linha={linha()} contexto={contexto()} />)

    expect(opcoesDoSelect()).toEqual(['', ALIMENTACAO, TRANSPORTE, CDB])
    expect(screen.getByRole('option', { name: 'CDB' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Resgate de CDB' })).not.toBeInTheDocument()
  })

  it('linha de RECEITA oferece receita e resgate, e nunca investimento', () => {
    render(<CelulaDeCategoria linha={linha({ kind: 'income' })} contexto={contexto()} />)

    expect(opcoesDoSelect()).toEqual(['', RESGATE])
    expect(screen.queryByRole('option', { name: 'CDB' })).not.toBeInTheDocument()
  })

  it('a sugestão de investimento aparece escolhida, como qualquer outra categoria', () => {
    const comSugestao = linha({
      suggestedCategoryId: CDB,
      matchScore: 92,
      matchedKeyword: 'cdb',
      description: 'CDB 15 DIAS',
    })
    render(<CelulaDeCategoria linha={comSugestao} contexto={contexto()} />)

    expect(screen.getByRole('combobox')).toHaveValue(CDB)
    // Sem tratamento visual especial: a natureza já está no nome do grupo
    // (spec 0006 §3.2.3).
    expect(screen.getByText('92% · «cdb»')).toBeInTheDocument()
  })
})

describe('rotuloDaLinha', () => {
  it('descrição, data curta e valor FALADO — nunca "traço cinquenta"', () => {
    // `Intl` põe um espaço inseparável (U+00A0) entre "R$" e o número.
    const NBSP = '\u00a0'
    expect(rotuloDaLinha(linha())).toBe(`MERCADO DO SEU JOSE, 03/07, R$${NBSP}50,00 negativos`)
    expect(rotuloDaLinha(linha({ kind: 'income', amountCents: 120000 }))).toBe(
      `MERCADO DO SEU JOSE, 03/07, R$${NBSP}1.200,00 positivos`,
    )
  })

  it('linha rejeitada sem valores fala do número da linha do arquivo', () => {
    expect(
      rotuloDaLinha(
        linha({
          status: 'rejeitado',
          kind: null,
          occurredOn: null,
          amountCents: null,
          description: null,
          lineNo: 9,
        }),
      ),
    ).toBe('linha 9 do arquivo, sem data, valor não lido')
  })
})
