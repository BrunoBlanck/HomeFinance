import { describe, expect, it } from 'vitest'
import { CHAVE_OUTRAS, dobrarParaRosca, type GrupoDaRosca, rotuloDeOutras } from './dobrar'

function grupo(key: string, cents: number, shareBp: number, pendente?: boolean): GrupoDaRosca {
  return { key, label: key, cents, shareBp, ...(pendente ? { pendente: true } : {}) }
}

describe('dobrarParaRosca', () => {
  it('um grupo só é o anel inteiro, no papel 1', () => {
    const { fatias, papelPorChave } = dobrarParaRosca([grupo('a', 10_000, 10_000)])
    expect(fatias).toEqual([{ key: 'a', label: 'a', cents: 10_000, shareBp: 10_000, papel: '1' }])
    expect(papelPorChave.get('a')).toBe('1')
  })

  it('até quatro nomeadas recebem 1…4 na ordem do servidor, sem Outras', () => {
    const { fatias } = dobrarParaRosca([
      grupo('a', 4000, 4000),
      grupo('b', 3000, 3000),
      grupo('c', 2000, 2000),
      grupo('d', 1000, 1000),
    ])
    expect(fatias.map((f) => [f.key, f.papel])).toEqual([
      ['a', '1'],
      ['b', '2'],
      ['c', '3'],
      ['d', '4'],
    ])
    expect(fatias.some((f) => f.papel === 'outras')).toBe(false)
  })

  it('a 5ª categoria vira "Outra (1 categoria)", por último', () => {
    const { fatias, papelPorChave } = dobrarParaRosca([
      grupo('a', 4000, 4000),
      grupo('b', 3000, 3000),
      grupo('c', 1500, 1500),
      grupo('d', 1000, 1000),
      grupo('e', 500, 500),
    ])
    expect(fatias).toHaveLength(5)
    expect(fatias.at(-1)).toEqual({
      key: CHAVE_OUTRAS,
      label: 'Outra (1 categoria)',
      cents: 500,
      shareBp: 500,
      papel: 'outras',
    })
    expect(papelPorChave.get('e')).toBe('outras')
  })

  it('da 5ª em diante tudo dobra numa fatia só, com SOMA INTEIRA de centavos e de bp', () => {
    const { fatias, papelPorChave } = dobrarParaRosca([
      grupo('a', 210_000, 4100),
      grupo('b', 120_000, 2340),
      grupo('c', 80_000, 1560),
      grupo('d', 40_000, 780),
      grupo('e', 30_001, 585),
      grupo('f', 20_003, 390),
      grupo('g', 12_999, 245),
    ])
    expect(fatias).toHaveLength(5)
    const outras = fatias.at(-1)
    expect(outras).toEqual({
      key: CHAVE_OUTRAS,
      label: 'Outras (3 categorias)',
      cents: 30_001 + 20_003 + 12_999,
      shareBp: 585 + 390 + 245,
      papel: 'outras',
    })
    // A soma das fatias visíveis continua fechando em 10000: a dobra não
    // perde nem inventa ponto-base.
    expect(fatias.reduce((soma, f) => soma + f.shareBp, 0)).toBe(10_000)
    for (const chave of ['e', 'f', 'g']) expect(papelPorChave.get(chave)).toBe('outras')
  })

  it('a pendente nunca dobra e não consome passo da rampa, onde quer que esteja', () => {
    // No meio: as quatro nomeadas continuam 1 → 4, e a 5ª nomeada dobra.
    const noMeio = dobrarParaRosca([
      grupo('a', 4000, 4000),
      grupo('b', 2000, 2000),
      grupo('sem', 1500, 1500, true),
      grupo('c', 1000, 1000),
      grupo('d', 900, 900),
      grupo('e', 600, 600),
    ])
    expect(noMeio.fatias.map((f) => [f.key, f.papel])).toEqual([
      ['a', '1'],
      ['b', '2'],
      ['sem', 'pendente'],
      ['c', '3'],
      ['d', '4'],
      [CHAVE_OUTRAS, 'outras'],
    ])
    expect(noMeio.fatias.at(-1)?.label).toBe('Outra (1 categoria)')

    // Por último (o caso comum): ela fica ANTES de Outras — Outras é sempre a
    // última do anel.
    const porUltimo = dobrarParaRosca([
      grupo('a', 3000, 3000),
      grupo('b', 2000, 2000),
      grupo('c', 1500, 1500),
      grupo('d', 1200, 1200),
      grupo('e', 1000, 1000),
      grupo('f', 800, 800),
      grupo('sem', 500, 500, true),
    ])
    expect(porUltimo.fatias.map((f) => f.papel)).toEqual(['1', '2', '3', '4', 'pendente', 'outras'])
    expect(porUltimo.fatias.at(-1)?.label).toBe('Outras (2 categorias)')
    expect(porUltimo.papelPorChave.get('sem')).toBe('pendente')
  })

  it('só a pendente também é anel inteiro — o mês que mais precisa do link', () => {
    const { fatias } = dobrarParaRosca([grupo('sem', 30_000, 10_000, true)])
    expect(fatias).toEqual([
      { key: 'sem', label: 'sem', cents: 30_000, shareBp: 10_000, papel: 'pendente' },
    ])
  })

  it('shareBp = 0 some do anel, não conta no N e não recebe papel', () => {
    const { fatias, papelPorChave } = dobrarParaRosca([
      grupo('a', 5000, 5000),
      grupo('b', 3000, 3000),
      grupo('c', 1000, 1000),
      grupo('d', 1000, 1000),
      grupo('zero', 1, 0),
      grupo('e', 0, 0),
    ])
    expect(fatias.map((f) => f.key)).toEqual(['a', 'b', 'c', 'd'])
    expect(papelPorChave.has('zero')).toBe(false)
    expect(papelPorChave.has('e')).toBe(false)
  })

  it('nada dentro, nada fora', () => {
    const { fatias, papelPorChave } = dobrarParaRosca([])
    expect(fatias).toEqual([])
    expect(papelPorChave.size).toBe(0)
  })

  it('teto real do anel: 4 nomeadas + pendente + Outras = 6', () => {
    const muitos = Array.from({ length: 12 }, (_, i) => grupo(`c${i}`, 1000 - i, 800 - i))
    const { fatias } = dobrarParaRosca([...muitos, grupo('sem', 10, 8, true)])
    expect(fatias).toHaveLength(6)
    expect(fatias.at(-1)?.label).toBe('Outras (8 categorias)')
  })
})

describe('rotuloDeOutras', () => {
  it('concorda em número', () => {
    expect(rotuloDeOutras(1)).toBe('Outra (1 categoria)')
    expect(rotuloDeOutras(2)).toBe('Outras (2 categorias)')
    expect(rotuloDeOutras(6)).toBe('Outras (6 categorias)')
  })
})
