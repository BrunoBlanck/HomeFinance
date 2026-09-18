import { describe, expect, it } from 'vitest'
import { aplicarNaBusca, validarBusca } from './search'

const UUID = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const OUTRO_UUID = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'

describe('validarBusca', () => {
  it('deixa passar mês, conta e filtro bem formados', () => {
    expect(validarBusca({ mes: '2026-08', conta: UUID, semCategoria: 1 })).toEqual({
      mes: '2026-08',
      conta: UUID,
      semCategoria: 1,
    })
  })

  it('aceita o filtro como string, que é como um link antigo pode trazê-lo', () => {
    expect(validarBusca({ semCategoria: '1' })).toEqual({ semCategoria: 1 })
  })

  it('descarta mês malformado', () => {
    expect(validarBusca({ mes: '2026-13' })).toEqual({})
    expect(validarBusca({ mes: '2026' })).toEqual({})
    expect(validarBusca({ mes: '<script>' })).toEqual({})
    expect(validarBusca({ mes: 42 })).toEqual({})
  })

  it('descarta conta que não tem forma de UUID', () => {
    // O rigor aqui é o mesmo do `mes`: a conta vai direto para uma query da
    // API, e `?conta=' OR 1=1 --` não pode sequer virar requisição.
    expect(validarBusca({ conta: 'acc-1' })).toEqual({})
    expect(validarBusca({ conta: "' OR 1=1 --" })).toEqual({})
    expect(validarBusca({ conta: '<script>alert(1)</script>' })).toEqual({})
    expect(validarBusca({ conta: '../../etc/passwd' })).toEqual({})
    expect(validarBusca({ conta: [UUID] })).toEqual({})
  })

  it('descarta qualquer valor de filtro que não seja 1', () => {
    expect(validarBusca({ semCategoria: 0 })).toEqual({})
    expect(validarBusca({ semCategoria: true })).toEqual({})
    expect(validarBusca({ semCategoria: 'sim' })).toEqual({})
    expect(validarBusca({ semCategoria: 2 })).toEqual({})
  })

  it('ignora chave que não conhece', () => {
    expect(validarBusca({ redirect: 'https://exemplo.invalido', mes: '2026-08' })).toEqual({
      mes: '2026-08',
    })
  })

  it('não quebra com busca vazia', () => {
    expect(validarBusca({})).toEqual({})
  })

  describe('natureza (/relatorios/categorias)', () => {
    it('deixa passar as duas palavras da allowlist', () => {
      expect(validarBusca({ natureza: 'despesas' })).toEqual({ natureza: 'despesas' })
      expect(validarBusca({ natureza: 'receitas' })).toEqual({ natureza: 'receitas' })
    })

    it('descarta qualquer outra coisa — a tela cai em despesas', () => {
      // O valor vai virar `kind=expense|income` na query da API, então nada
      // fora da lista pode sequer chegar à tela.
      expect(validarBusca({ natureza: 'expense' })).toEqual({})
      expect(validarBusca({ natureza: 'Receitas' })).toEqual({})
      expect(validarBusca({ natureza: 'tudo' })).toEqual({})
      expect(validarBusca({ natureza: "' OR 1=1 --" })).toEqual({})
      expect(validarBusca({ natureza: '<script>alert(1)</script>' })).toEqual({})
      expect(validarBusca({ natureza: ['receitas'] })).toEqual({})
      expect(validarBusca({ natureza: 1 })).toEqual({})
      expect(validarBusca({ natureza: '' })).toEqual({})
    })

    it('convive com o mês, sem apagar um ao outro', () => {
      expect(validarBusca({ mes: '2026-09', natureza: 'receitas' })).toEqual({
        mes: '2026-09',
        natureza: 'receitas',
      })
    })
  })

  describe('contraparte (/transferencias)', () => {
    it('deixa passar a contraparte quando há conta e as duas são UUIDs distintos', () => {
      expect(validarBusca({ conta: UUID, contraparte: OUTRO_UUID })).toEqual({
        conta: UUID,
        contraparte: OUTRO_UUID,
      })
    })

    it('descarta a contraparte sem conta — "entre X e Y" precisa do X', () => {
      expect(validarBusca({ contraparte: OUTRO_UUID })).toEqual({})
    })

    it('descarta a contraparte quando a conta foi descartada por ser inválida', () => {
      expect(validarBusca({ conta: 'acc-1', contraparte: OUTRO_UUID })).toEqual({})
    })

    it('descarta a contraparte igual à conta — o servidor responderia 400', () => {
      expect(validarBusca({ conta: UUID, contraparte: UUID })).toEqual({ conta: UUID })
    })

    it('descarta contraparte que não tem forma de UUID', () => {
      expect(validarBusca({ conta: UUID, contraparte: "' OR 1=1 --" })).toEqual({ conta: UUID })
      expect(validarBusca({ conta: UUID, contraparte: [OUTRO_UUID] })).toEqual({ conta: UUID })
    })
  })
})

describe('aplicarNaBusca', () => {
  it('apagar a conta apaga a contraparte junto', () => {
    expect(
      aplicarNaBusca(
        { mes: '2026-09', conta: UUID, contraparte: OUTRO_UUID },
        { conta: undefined },
      ),
    ).toEqual({ mes: '2026-09' })
  })

  it('trocar a conta pela própria contraparte desfaz o par', () => {
    expect(aplicarNaBusca({ conta: UUID, contraparte: OUTRO_UUID }, { conta: OUTRO_UUID })).toEqual(
      { conta: OUTRO_UUID },
    )
  })

  it('apagar só a contraparte mantém a conta', () => {
    expect(
      aplicarNaBusca({ conta: UUID, contraparte: OUTRO_UUID }, { contraparte: undefined }),
    ).toEqual({ conta: UUID })
  })

  it('materializa undefined em ausência, sem deixar chave vazia na URL', () => {
    const proxima = aplicarNaBusca({ mes: '2026-09', semCategoria: 1 }, { semCategoria: undefined })
    expect(proxima).toEqual({ mes: '2026-09' })
    expect('semCategoria' in proxima).toBe(false)
  })

  it('trocar a natureza mantém o mês; voltar para despesas apaga a chave', () => {
    expect(aplicarNaBusca({ mes: '2026-09' }, { natureza: 'receitas' })).toEqual({
      mes: '2026-09',
      natureza: 'receitas',
    })
    // Despesas é o padrão: a URL canônica não a escreve.
    const proxima = aplicarNaBusca(
      { mes: '2026-09', natureza: 'receitas' },
      { natureza: undefined },
    )
    expect(proxima).toEqual({ mes: '2026-09' })
    expect('natureza' in proxima).toBe(false)
  })
})
