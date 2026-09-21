import { describe, expect, it } from 'vitest'
import type { CategoryReportChild, CategoryReportGroup } from '@/api/types'
import type { PapelDaFatia } from '@/components/DonutChart/DonutChart'
import { linhasDoGrupo } from './CategoryReportScreen'

/** As linhas planas da tabela, por grupo (docs/DESIGN.md E6a (e)).
 *
 *  A tela já é testada de ponta a ponta com React Testing Library; o que falta
 *  ali é a combinação `grupo COM filhas e directCount == 0` — a regra diz que
 *  "Sem subcategoria" só aparece quando o grupo tem filhas **e** houve
 *  lançamento direto nele, e as duas metades da conjunção precisam de teste
 *  próprio. Testar a função exportada, e não a tela, é o que deixa as quatro
 *  combinações caberem num arquivo. */

function filha(over: Partial<CategoryReportChild> = {}): CategoryReportChild {
  return {
    categoryId: 'f-1',
    name: 'Mercado',
    archivedAt: null,
    totalCents: 1_000,
    count: 2,
    shareBp: 2_500,
    ...over,
  }
}

function grupo(over: Partial<CategoryReportGroup> = {}): CategoryReportGroup {
  return {
    categoryId: 'g-1',
    name: 'Alimentação',
    archivedAt: null,
    totalCents: 4_000,
    count: 8,
    shareBp: 10_000,
    directCents: 0,
    directCount: 0,
    directShareBp: 0,
    children: [],
    ...over,
  }
}

const SEM_PAPEL: ReadonlyMap<string, PapelDaFatia> = new Map()

describe('linhasDoGrupo', () => {
  it('grupo FOLHA (sem filhas) é uma linha só, sem "Sem subcategoria"', () => {
    const linhas = linhasDoGrupo(
      grupo({ children: [], directCents: 4_000, directCount: 8, directShareBp: 10_000 }),
      SEM_PAPEL,
    )

    expect(linhas).toHaveLength(1)
    expect(linhas[0]).toMatchObject({ tipo: 'grupo', nome: 'Alimentação', cents: 4_000, count: 8 })
  })

  it('grupo COM filhas e directCount ZERO não ganha a linha "Sem subcategoria"', () => {
    const linhas = linhasDoGrupo(
      grupo({
        children: [filha({ categoryId: 'f-1', name: 'Mercado', totalCents: 3_000, count: 5 })],
        directCents: 0,
        directCount: 0,
        directShareBp: 0,
      }),
      SEM_PAPEL,
    )

    expect(linhas.map((l) => l.tipo)).toEqual(['grupo', 'filha'])
    expect(linhas.some((l) => l.nome === 'Sem subcategoria')).toBe(false)
  })

  it('grupo COM filhas e lançamento direto ganha a linha, com os números de direct*', () => {
    const linhas = linhasDoGrupo(
      grupo({
        children: [filha({ totalCents: 3_000, count: 5, shareBp: 7_500 })],
        directCents: 1_000,
        directCount: 3,
        directShareBp: 2_500,
      }),
      SEM_PAPEL,
    )

    expect(linhas.map((l) => l.tipo)).toEqual(['grupo', 'filha', 'direta'])
    expect(linhas[2]).toMatchObject({
      tipo: 'direta',
      nome: 'Sem subcategoria',
      grupo: 'Alimentação',
      cents: 1_000,
      count: 3,
      shareBp: 2_500,
    })
    // A coluna fecha visivelmente: filha + direta = o grupo.
    const filhas = linhas.slice(1)
    expect(filhas.reduce((s, l) => s + l.shareBp, 0)).toBe(linhas[0]?.shareBp)
    expect(filhas.reduce((s, l) => s + l.cents, 0)).toBe(linhas[0]?.cents)
  })

  it('grupo com direct > 0 mas SEM filhas continua com uma linha só', () => {
    const linhas = linhasDoGrupo(
      grupo({ children: [], directCents: 4_000, directCount: 8, directShareBp: 10_000 }),
      SEM_PAPEL,
    )
    expect(linhas).toHaveLength(1)
  })

  it('o balde vira "Sem categoria", marcado para o link Categorizar', () => {
    const linhas = linhasDoGrupo(
      grupo({
        categoryId: null,
        name: null,
        children: [],
        directCents: 4_000,
        directCount: 8,
        directShareBp: 10_000,
      }),
      new Map([['sem-categoria', 'pendente']]),
    )

    expect(linhas).toHaveLength(1)
    expect(linhas[0]).toMatchObject({
      chave: 'g:sem-categoria',
      nome: 'Sem categoria',
      semCategoria: true,
      papel: 'pendente',
    })
  })

  it('arquivada é marcada no grupo e na filha, cada uma por si', () => {
    const linhas = linhasDoGrupo(
      grupo({
        archivedAt: '2026-08-01T10:30:00Z',
        children: [
          filha({ categoryId: 'f-1', name: 'Viva', archivedAt: null }),
          filha({ categoryId: 'f-2', name: 'Morta', archivedAt: '2026-08-01T10:30:00Z' }),
        ],
      }),
      SEM_PAPEL,
    )

    expect(linhas.map((l) => [l.nome, l.arquivada])).toEqual([
      ['Alimentação', true],
      ['Viva', false],
      ['Morta', true],
    ])
  })

  it('a chave de cada linha é única dentro do grupo', () => {
    const linhas = linhasDoGrupo(
      grupo({
        children: [filha({ categoryId: 'f-1' }), filha({ categoryId: 'f-2', name: 'Feira' })],
        directCents: 10,
        directCount: 1,
        directShareBp: 10,
      }),
      SEM_PAPEL,
    )
    expect(new Set(linhas.map((l) => l.chave)).size).toBe(linhas.length)
  })
})
