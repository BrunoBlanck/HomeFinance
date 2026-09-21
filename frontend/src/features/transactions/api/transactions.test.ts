import { describe, expect, it } from 'vitest'
import type { TipoDeLancamento } from '@/app/search'
import { grupoDeTipo, transactionsQueryKey } from './transactions'

const TIPOS: readonly TipoDeLancamento[] = [
  'receitas',
  'despesas',
  'transferencias',
  'investimentos',
]

/** A tradução da palavra da URL para o valor do contrato.
 *
 *  Merece teste próprio por causa do jeito ESPECÍFICO como ela falha: mandar
 *  `kindGroup=despesas` faria o servidor ignorar o parâmetro desconhecido e
 *  devolver a janela inteira, enquanto o seletor da tela continuaria afirmando
 *  "Despesas". Nada estoura; a tela só mente. */
describe('grupoDeTipo', () => {
  it('traduz as cinco opções da tela para o contrato', () => {
    // Tudo é a AUSÊNCIA da chave: `kindGroup=all` não existe e é 400.
    expect(grupoDeTipo(undefined)).toBeUndefined()
    expect(grupoDeTipo('receitas')).toBe('income')
    expect(grupoDeTipo('despesas')).toBe('expense')
    expect(grupoDeTipo('transferencias')).toBe('transfer')
    expect(grupoDeTipo('investimentos')).toBe('investment')
  })

  it('nunca deixa a palavra da URL vazar para a API', () => {
    const doContrato = ['income', 'expense', 'transfer', 'investment']
    for (const tipo of TIPOS) {
      const grupo = grupoDeTipo(tipo)
      expect(grupo).not.toBe(tipo)
      expect(doContrato).toContain(grupo)
    }
  })

  it('os quatro grupos são distintos — nenhuma opção cai na de outra', () => {
    const grupos = TIPOS.map(grupoDeTipo)
    expect(new Set(grupos).size).toBe(TIPOS.length)
  })
})

/** É a chave da query que faz a troca de filtro **descartar o cursor**: chave
 *  nova, `infiniteQuery` nova, primeira página. O cursor é posição, não filtro
 *  (ADR-030d) — continuá-lo em outro recorte pularia o começo da lista nova. */
describe('transactionsQueryKey', () => {
  it('dá uma chave diferente a cada opção do filtro', () => {
    const chaves = [undefined, ...TIPOS].map((tipo) =>
      JSON.stringify(transactionsQueryKey({ mes: '2026-09', ...(tipo ? { tipo } : {}) })),
    )
    expect(new Set(chaves).size).toBe(5)
  })

  it('sem filtro nenhum, toda dimensão é `null` — o cache de Tudo não se parte', () => {
    expect(transactionsQueryKey({ mes: '2026-09' })).toEqual([
      'transactions',
      { mes: '2026-09', contaId: null, tipo: null, categoriaId: null },
    ])
  })

  it('a categoria é uma dimensão própria da chave — entrar numa não serve a anterior', () => {
    const umaCategoria = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
    const outra = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'
    const chaves = [
      JSON.stringify(transactionsQueryKey({ mes: '2026-09' })),
      JSON.stringify(transactionsQueryKey({ mes: '2026-09', categoriaId: umaCategoria })),
      JSON.stringify(transactionsQueryKey({ mes: '2026-09', categoriaId: outra })),
      JSON.stringify(
        transactionsQueryKey({ mes: '2026-09', categoriaId: umaCategoria, tipo: 'despesas' }),
      ),
    ]
    expect(new Set(chaves).size).toBe(4)
  })

  it('separa também por conta, sem confundir as duas dimensões', () => {
    const contaId = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
    const comConta = JSON.stringify(transactionsQueryKey({ mes: '2026-09', contaId }))
    const comTipo = JSON.stringify(transactionsQueryKey({ mes: '2026-09', tipo: 'despesas' }))
    const comOsDois = JSON.stringify(
      transactionsQueryKey({ mes: '2026-09', contaId, tipo: 'despesas' }),
    )
    expect(new Set([comConta, comTipo, comOsDois]).size).toBe(3)
  })
})
