import { describe, expect, it } from 'vitest'
import { CHAVE_OUTRAS, dobrarParaRosca, type GrupoDaRosca } from './dobrar'

/** Bordas da dobra encontradas na validação de QA da E6a.
 *
 *  O que faltava em `dobrar.test.ts`: a fronteira dos 4 nomeados **com a
 *  pendente presente** (a pendente não consome passo da rampa, então 4, 5 e 6
 *  nomeados + pendente têm de dar 5, 6 e 6 fatias), o mês inteiro empatado, o
 *  balde em primeiro lugar e a fatia de participação zero no meio de uma soma
 *  que fecha em 10000. */

function grupo(key: string, cents: number, shareBp: number, pendente?: boolean): GrupoDaRosca {
  return { key, label: key, cents, shareBp, ...(pendente ? { pendente: true } : {}) }
}

/** N grupos nomeados com participação igual, mais a pendente no fim. */
function nomeadasComPendente(n: number): GrupoDaRosca[] {
  const bp = Math.floor(10_000 / (n + 1))
  const lista = Array.from({ length: n }, (_, i) => grupo(`n${i}`, 1_000, bp))
  lista.push(grupo('pendente', 1_000, 10_000 - bp * n, true))
  return lista
}

describe('dobrarParaRosca — a fronteira da dobra', () => {
  it('4 nomeadas + pendente: 5 fatias, nenhuma dobrada', () => {
    const { fatias, papelPorChave } = dobrarParaRosca(nomeadasComPendente(4))

    expect(fatias).toHaveLength(5)
    expect(fatias.map((f) => f.papel)).toEqual(['1', '2', '3', '4', 'pendente'])
    expect(papelPorChave.get('pendente')).toBe('pendente')
    // A pendente é a última porque o servidor a pôs lá — não porque dobrou.
    expect(fatias.at(-1)?.key).toBe('pendente')
  })

  it('5 nomeadas + pendente: 6 fatias, a 5ª vira "Outra (1 categoria)" no fim', () => {
    const { fatias, papelPorChave } = dobrarParaRosca(nomeadasComPendente(5))

    expect(fatias).toHaveLength(6)
    expect(fatias.map((f) => f.papel)).toEqual(['1', '2', '3', '4', 'pendente', 'outras'])
    expect(fatias.at(-1)?.key).toBe(CHAVE_OUTRAS)
    expect(fatias.at(-1)?.label).toBe('Outra (1 categoria)')
    expect(papelPorChave.get('n4')).toBe('outras')
  })

  it('6 nomeadas + pendente: continua em 6 fatias — o teto do anel não cede', () => {
    const { fatias, papelPorChave } = dobrarParaRosca(nomeadasComPendente(6))

    expect(fatias).toHaveLength(6)
    expect(fatias.at(-1)?.label).toBe('Outras (2 categorias)')
    // As duas dobradas continuam recebendo papel: é o que põe a hachura na
    // linha delas na tabela.
    expect(papelPorChave.get('n4')).toBe('outras')
    expect(papelPorChave.get('n5')).toBe('outras')
    // E a tabela lista TODAS: a dobra é só do anel.
    expect(papelPorChave.size).toBe(7)
  })

  it('20 nomeadas + pendente: 6 fatias, e a dobrada soma as 16 restantes', () => {
    const entrada = Array.from({ length: 20 }, (_, i) => grupo(`n${i}`, 100, 500))
    entrada.push(grupo('pendente', 100, 0, true))
    // A pendente com bp 0 não é desenhada — é o caso "mês sem pendência".
    const { fatias } = dobrarParaRosca(entrada)

    expect(fatias).toHaveLength(5)
    expect(fatias.at(-1)).toMatchObject({
      key: CHAVE_OUTRAS,
      label: 'Outras (16 categorias)',
      cents: 1_600,
      shareBp: 8_000,
    })
  })
})

describe('dobrarParaRosca — empates e extremos', () => {
  it('todos os grupos com o MESMO valor: a ordem do servidor é a ordem do anel', () => {
    const entrada = ['a', 'b', 'c', 'd', 'e', 'f'].map((k) => grupo(k, 1_000, 1_666))
    const { fatias, papelPorChave } = dobrarParaRosca(entrada)

    expect(fatias.map((f) => f.key)).toEqual(['a', 'b', 'c', 'd', CHAVE_OUTRAS])
    expect(papelPorChave.get('a')).toBe('1')
    expect(papelPorChave.get('d')).toBe('4')
    expect(fatias.at(-1)?.shareBp).toBe(1_666 * 2)
  })

  it('"Sem categoria" sendo o MAIOR valor fica em primeiro e não gasta passo da rampa', () => {
    const { fatias, papelPorChave } = dobrarParaRosca([
      grupo('pendente', 90_000, 9_000, true),
      grupo('a', 5_000, 500),
      grupo('b', 3_000, 300),
      grupo('c', 1_000, 100),
      grupo('d', 500, 50),
      grupo('e', 500, 50),
    ])

    expect(fatias[0]?.papel).toBe('pendente')
    // As cinco nomeadas continuam 1…4 + Outras: a pendente não roubou o '1'.
    expect(fatias.map((f) => f.papel)).toEqual(['pendente', '1', '2', '3', '4', 'outras'])
    expect(papelPorChave.get('a')).toBe('1')
    expect(fatias).toHaveLength(6)
  })

  it('uma fatia de participação ZERO some do anel sem furar a soma de 10000', () => {
    const entrada = [
      grupo('a', 9_999, 9_999),
      grupo('zero', 0, 0), // lançamento de R$ 0,00 numa categoria própria
      grupo('b', 1, 1),
    ]
    const { fatias, papelPorChave } = dobrarParaRosca(entrada)

    expect(fatias.map((f) => f.key)).toEqual(['a', 'b'])
    expect(papelPorChave.has('zero')).toBe(false)
    expect(fatias.reduce((soma, f) => soma + f.shareBp, 0)).toBe(10_000)
    // E o zero não contou no N de Outras, porque nem chegou a dobrar.
    expect(fatias.some((f) => f.key === CHAVE_OUTRAS)).toBe(false)
  })

  it('zeros ENTRE as nomeadas não consomem passo da rampa', () => {
    const { fatias, papelPorChave } = dobrarParaRosca([
      grupo('a', 5_000, 5_000),
      grupo('z1', 0, 0),
      grupo('b', 3_000, 3_000),
      grupo('z2', 0, 0),
      grupo('c', 1_000, 1_000),
      grupo('d', 1_000, 1_000),
      grupo('e', 0, 0),
    ])

    expect(fatias.map((f) => f.papel)).toEqual(['1', '2', '3', '4'])
    expect(papelPorChave.size).toBe(4)
  })
})
