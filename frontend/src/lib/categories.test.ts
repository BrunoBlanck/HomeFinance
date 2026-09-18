import { describe, expect, it } from 'vitest'
import type { Category, CategoryTree } from '@/api/types'
import {
  categoriaPorId,
  ladoDaNatureza,
  mesmoLadoDoDinheiro,
  opcoesDeCategoria,
} from './categories'

/** O pareamento por LADO DO DINHEIRO (ADR-029b) é a regra que a E7 trouxe e a
 *  única cópia dela no frontend mora aqui. Os três seletores do produto —
 *  `/lancamentos`, a revisão da importação e o formulário de categoria — saem
 *  desta função; se ela errar, a pessoa vê a sugestão de investimento na
 *  revisão e não consegue escolhê-la à mão. */

function cat(over: Partial<Category> & Pick<Category, 'id' | 'name' | 'kind'>): Category {
  return {
    parentId: null,
    keywords: [],
    archivedAt: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    children: [],
    ...over,
  }
}

const MORADIA = cat({
  id: 'grp-moradia',
  name: 'Moradia',
  kind: 'expense',
  children: [cat({ id: 'sub-energia', name: 'Energia', kind: 'expense', parentId: 'grp-moradia' })],
})
const LAZER = cat({ id: 'grp-lazer', name: 'Lazer', kind: 'expense' })
const SALARIO = cat({ id: 'grp-salario', name: 'Salário', kind: 'income' })
const INVESTIMENTOS = cat({
  id: 'grp-investimentos',
  name: 'Investimentos',
  kind: 'investment',
  children: [
    cat({ id: 'sub-cdb', name: 'CDB', kind: 'investment', parentId: 'grp-investimentos' }),
    cat({ id: 'sub-tesouro', name: 'Tesouro', kind: 'investment', parentId: 'grp-investimentos' }),
  ],
})
const RESGATES = cat({ id: 'grp-resgates', name: 'Resgates', kind: 'redemption' })

const ARVORE: CategoryTree = {
  expense: [MORADIA, LAZER],
  income: [SALARIO],
  investment: [INVESTIMENTOS],
  redemption: [RESGATES],
}

describe('opcoesDeCategoria — o lado do dinheiro, não a natureza', () => {
  // Critério de aceite 2 da spec 0006.
  it('despesa oferece as categorias de despesa E as de investimento, e nunca as do outro lado', () => {
    const opcoes = opcoesDeCategoria(ARVORE, 'expense')

    expect(opcoes.map((o) => o.value)).toEqual([
      'sub-energia',
      'grp-lazer',
      'sub-cdb',
      'sub-tesouro',
    ])
    // O grupo vira `optgroup`; o grupo sem filhas vira opção ele mesmo.
    expect(opcoes).toContainEqual({ value: 'sub-cdb', label: 'CDB', group: 'Investimentos' })
    expect(opcoes).toContainEqual({ value: 'grp-lazer', label: 'Lazer' })

    // Receita e resgate ficam do outro lado: oferecê-los é oferecer um 422.
    expect(opcoes.map((o) => o.value)).not.toContain('grp-salario')
    expect(opcoes.map((o) => o.value)).not.toContain('grp-resgates')
  })

  it('receita oferece as categorias de receita E as de resgate, e nunca as do outro lado', () => {
    const opcoes = opcoesDeCategoria(ARVORE, 'income')

    expect(opcoes.map((o) => o.value)).toEqual(['grp-salario', 'grp-resgates'])
    expect(opcoes.map((o) => o.value)).not.toContain('sub-cdb')
    expect(opcoes.map((o) => o.value)).not.toContain('grp-lazer')
  })

  it('a ordem preserva a do servidor, com o lado do dia a dia antes do investimento', () => {
    const opcoes = opcoesDeCategoria(ARVORE, 'expense')
    const posicaoDeLazer = opcoes.findIndex((o) => o.value === 'grp-lazer')
    const posicaoDeCdb = opcoes.findIndex((o) => o.value === 'sub-cdb')
    expect(posicaoDeLazer).toBeLessThan(posicaoDeCdb)
  })

  it('árvore ausente ou sem as naturezas novas devolve o que houver, sem quebrar', () => {
    expect(opcoesDeCategoria(undefined, 'expense')).toEqual([])
    // Uma árvore em cache, gravada antes de a E7 existir, não tem os dois
    // arrays novos — e a tela continua oferecendo o que ela tem.
    const antiga = { expense: [LAZER], income: [SALARIO] } as unknown as CategoryTree
    expect(opcoesDeCategoria(antiga, 'expense').map((o) => o.value)).toEqual(['grp-lazer'])
  })
})

describe('categoriaPorId — as quatro naturezas', () => {
  it('acha o grupo e a folha em qualquer uma das quatro', () => {
    expect(categoriaPorId(ARVORE, 'grp-lazer')?.name).toBe('Lazer')
    expect(categoriaPorId(ARVORE, 'sub-energia')?.name).toBe('Energia')
    expect(categoriaPorId(ARVORE, 'grp-salario')?.name).toBe('Salário')
    expect(categoriaPorId(ARVORE, 'sub-tesouro')?.name).toBe('Tesouro')
    expect(categoriaPorId(ARVORE, 'grp-resgates')?.name).toBe('Resgates')
  })

  it('id desconhecido, vazio ou nulo devolve undefined', () => {
    expect(categoriaPorId(ARVORE, 'nao-existe')).toBeUndefined()
    expect(categoriaPorId(ARVORE, null)).toBeUndefined()
    expect(categoriaPorId(undefined, 'grp-lazer')).toBeUndefined()
  })

  it('não quebra numa árvore sem as naturezas novas', () => {
    const antiga = { expense: [LAZER], income: [] } as unknown as CategoryTree
    expect(categoriaPorId(antiga, 'grp-lazer')?.name).toBe('Lazer')
    expect(categoriaPorId(antiga, 'sub-cdb')).toBeUndefined()
  })
})

describe('lado do dinheiro de uma natureza (ADR-029c)', () => {
  it('aporte fica do lado da despesa e resgate do lado da receita', () => {
    expect(ladoDaNatureza('expense')).toBe('expense')
    expect(ladoDaNatureza('investment')).toBe('expense')
    expect(ladoDaNatureza('income')).toBe('income')
    expect(ladoDaNatureza('redemption')).toBe('income')
  })

  it('mesmoLadoDoDinheiro separa a troca permitida da recusada', () => {
    // Permitidas mesmo com a categoria em uso.
    expect(mesmoLadoDoDinheiro('expense', 'investment')).toBe(true)
    expect(mesmoLadoDoDinheiro('investment', 'expense')).toBe(true)
    expect(mesmoLadoDoDinheiro('income', 'redemption')).toBe(true)
    // Cruzar o lado: o servidor recusa assim que houver uso ou filha.
    expect(mesmoLadoDoDinheiro('expense', 'income')).toBe(false)
    expect(mesmoLadoDoDinheiro('investment', 'redemption')).toBe(false)
    expect(mesmoLadoDoDinheiro('redemption', 'expense')).toBe(false)
  })
})
