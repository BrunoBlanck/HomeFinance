import { describe, expect, it } from 'vitest'
import {
  aplicarNaBusca,
  NATUREZAS,
  naturezaValida,
  tamanhoDaJanelaValido,
  validarBusca,
} from './search'

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
    it('deixa passar as quatro palavras da allowlist', () => {
      expect(validarBusca({ natureza: 'despesas' })).toEqual({ natureza: 'despesas' })
      expect(validarBusca({ natureza: 'despesas-credito' })).toEqual({
        natureza: 'despesas-credito',
      })
      expect(validarBusca({ natureza: 'despesas-debito' })).toEqual({
        natureza: 'despesas-debito',
      })
      expect(validarBusca({ natureza: 'receitas' })).toEqual({ natureza: 'receitas' })
    })

    it('a ordem da allowlist é a ordem do seletor da tela', () => {
      expect(NATUREZAS).toEqual(['despesas', 'despesas-credito', 'despesas-debito', 'receitas'])
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

    it('descarta as formas quase certas dos recortes de conta (ADR-032)', () => {
      // O valor da API na URL é o erro de quem copia o parâmetro errado — e
      // `accountGroup=credit` só nasce da TRADUÇÃO da tela, nunca da URL.
      expect(validarBusca({ natureza: 'credit' })).toEqual({})
      expect(validarBusca({ natureza: 'debit' })).toEqual({})
      // Caixa, separador e espaço sobrando são outros valores.
      expect(validarBusca({ natureza: 'Despesas-Credito' })).toEqual({})
      expect(validarBusca({ natureza: 'despesas_credito' })).toEqual({})
      expect(validarBusca({ natureza: 'despesas-credito ' })).toEqual({})
      expect(validarBusca({ natureza: ' despesas-debito' })).toEqual({})
      // `?natureza=despesas-credito&natureza=receitas` colado à mão chega como
      // array e some INTEIRO — é o que garante que a tela nunca produz
      // `accountGroup` duas vezes na query (400 no servidor).
      expect(validarBusca({ natureza: ['despesas-credito'] })).toEqual({})
      expect(validarBusca({ natureza: ['despesas-credito', 'receitas'] })).toEqual({})
    })

    it('naturezaValida é a mesma allowlist, exposta para o <select> da tela', () => {
      expect(naturezaValida('despesas')).toBe('despesas')
      expect(naturezaValida('despesas-credito')).toBe('despesas-credito')
      expect(naturezaValida('despesas-debito')).toBe('despesas-debito')
      expect(naturezaValida('receitas')).toBe('receitas')
      expect(naturezaValida('credit')).toBeUndefined()
      expect(naturezaValida('')).toBeUndefined()
      expect(naturezaValida(undefined)).toBeUndefined()
      expect(naturezaValida(['despesas-credito'])).toBeUndefined()
    })

    it('convive com o mês, sem apagar um ao outro', () => {
      expect(validarBusca({ mes: '2026-09', natureza: 'receitas' })).toEqual({
        mes: '2026-09',
        natureza: 'receitas',
      })
    })
  })

  describe('tipo (/lancamentos, E2d)', () => {
    it('deixa passar as quatro palavras da allowlist', () => {
      expect(validarBusca({ tipo: 'receitas' })).toEqual({ tipo: 'receitas' })
      expect(validarBusca({ tipo: 'despesas' })).toEqual({ tipo: 'despesas' })
      expect(validarBusca({ tipo: 'transferencias' })).toEqual({ tipo: 'transferencias' })
      expect(validarBusca({ tipo: 'investimentos' })).toEqual({ tipo: 'investimentos' })
    })

    it('descarta qualquer outra coisa — a tela abre em Tudo', () => {
      // `tudo` NÃO existe: a ausência da chave é o padrão, e `kindGroup=all`
      // responderia 400 no servidor.
      expect(validarBusca({ tipo: 'tudo' })).toEqual({})
      // O valor da API na URL é o erro de quem copia o parâmetro errado.
      expect(validarBusca({ tipo: 'expense' })).toEqual({})
      expect(validarBusca({ tipo: 'income' })).toEqual({})
      expect(validarBusca({ tipo: 'transfer' })).toEqual({})
      expect(validarBusca({ tipo: 'investment' })).toEqual({})
      // Caixa diferente é outro valor.
      expect(validarBusca({ tipo: 'Despesas' })).toEqual({})
      expect(validarBusca({ tipo: 'TRANSFERENCIAS' })).toEqual({})
      // E o lixo, que não pode sequer virar requisição.
      expect(validarBusca({ tipo: "' OR 1=1 --" })).toEqual({})
      expect(validarBusca({ tipo: '<script>alert(1)</script>' })).toEqual({})
      expect(validarBusca({ tipo: ['despesas'] })).toEqual({})
      expect(validarBusca({ tipo: 1 })).toEqual({})
      expect(validarBusca({ tipo: '' })).toEqual({})
    })

    it('convive com mês, conta e o filtro de pendência', () => {
      expect(
        validarBusca({ mes: '2026-09', conta: UUID, tipo: 'despesas', semCategoria: 1 }),
      ).toEqual({ mes: '2026-09', conta: UUID, tipo: 'despesas', semCategoria: 1 })
    })

    /** A combinação sem resultado possível: transferência não tem categoria por
     *  desenho, e aporte e resgate têm por definição. Uma URL colada não pode
     *  virar lista vazia sem saída. */
    it('descarta o semCategoria sob transferências e investimentos', () => {
      expect(validarBusca({ tipo: 'transferencias', semCategoria: 1 })).toEqual({
        tipo: 'transferencias',
      })
      expect(validarBusca({ tipo: 'investimentos', semCategoria: '1' })).toEqual({
        tipo: 'investimentos',
      })
      // Nos outros dois ele sobrevive.
      expect(validarBusca({ tipo: 'receitas', semCategoria: 1 })).toEqual({
        tipo: 'receitas',
        semCategoria: 1,
      })
    })

    it('o tipo inválido não protege o semCategoria — a chave descartada não filtra', () => {
      // `?tipo=transfer&semCategoria=1` cai em Tudo COM o filtro de pendência:
      // o descarte é do par, não do valor cru.
      expect(validarBusca({ tipo: 'transfer', semCategoria: 1 })).toEqual({ semCategoria: 1 })
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

  it('trocar o tipo mantém o mês; voltar para Tudo apaga a chave', () => {
    expect(aplicarNaBusca({ mes: '2026-09' }, { tipo: 'despesas' })).toEqual({
      mes: '2026-09',
      tipo: 'despesas',
    })
    // Tudo é o padrão: a URL canônica não o escreve.
    const proxima = aplicarNaBusca({ mes: '2026-09', tipo: 'despesas' }, { tipo: undefined })
    expect(proxima).toEqual({ mes: '2026-09' })
    expect('tipo' in proxima).toBe(false)
  })

  it('entrar numa categoria desliga o filtro de pendência, nas duas pontas', () => {
    // "Só os sem categoria" DENTRO de uma categoria é conjunto vazio por
    // construção — a categoria vence, e a tela não escreve na URL o par que a
    // leitura seguinte limparia.
    expect(aplicarNaBusca({ mes: '2026-09', semCategoria: 1 }, { categoria: UUID })).toEqual({
      mes: '2026-09',
      categoria: UUID,
    })
    expect(validarBusca({ mes: '2026-09', categoria: UUID, semCategoria: 1 })).toEqual({
      mes: '2026-09',
      categoria: UUID,
    })
    // Sair da categoria devolve a pendência à mesa.
    expect(aplicarNaBusca({ categoria: UUID }, { categoria: undefined, semCategoria: 1 })).toEqual({
      semCategoria: 1,
    })
  })

  it('a categoria convive com tipo e conta — são dimensões independentes', () => {
    expect(
      validarBusca({ mes: '2026-09', conta: UUID, tipo: 'despesas', categoria: OUTRO_UUID }),
    ).toEqual({ mes: '2026-09', conta: UUID, tipo: 'despesas', categoria: OUTRO_UUID })
    // E o que não é uuid some, como em `conta`.
    expect(validarBusca({ categoria: 'alimentacao' })).toEqual({})
    expect(validarBusca({ categoria: 12 })).toEqual({})
  })

  it('trocar para transferências desliga o filtro de pendência', () => {
    // O mesmo descarte da validação, do outro lado do portão: a tela não pode
    // ESCREVER na URL o par que a próxima leitura limparia.
    expect(aplicarNaBusca({ mes: '2026-09', semCategoria: 1 }, { tipo: 'transferencias' })).toEqual(
      { mes: '2026-09', tipo: 'transferencias' },
    )
    expect(aplicarNaBusca({ semCategoria: 1 }, { tipo: 'investimentos' })).toEqual({
      tipo: 'investimentos',
    })
    // E ligar o filtro sob despesas continua valendo.
    expect(aplicarNaBusca({ tipo: 'despesas' }, { semCategoria: 1 })).toEqual({
      tipo: 'despesas',
      semCategoria: 1,
    })
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

  it('os recortes de conta entram e saem da URL como as outras naturezas', () => {
    expect(aplicarNaBusca({ mes: '2026-09' }, { natureza: 'despesas-credito' })).toEqual({
      mes: '2026-09',
      natureza: 'despesas-credito',
    })
    expect(
      aplicarNaBusca(
        { mes: '2026-09', natureza: 'despesas-credito' },
        { natureza: 'despesas-debito' },
      ),
    ).toEqual({ mes: '2026-09', natureza: 'despesas-debito' })
    // Voltar para Despesas (todas as contas) apaga a chave, sem deixar
    // `?natureza=` vazio.
    const proxima = aplicarNaBusca(
      { mes: '2026-09', natureza: 'despesas-debito' },
      { natureza: undefined },
    )
    expect(proxima).toEqual({ mes: '2026-09' })
    expect('natureza' in proxima).toBe(false)
  })
})

/** `?meses=` — o tamanho da janela de trabalho de `/ia` (spec 0010 §2.1).
 *
 *  A allowlist é 1, 2 e 3, e o motivo é do servidor: janela de mais de 3 meses
 *  é 400. Valor fora dela **some** — a URL é editável, e um link truncado tem
 *  de abrir o app na janela padrão, não uma tela de erro. */
describe('meses — a janela de trabalho de /ia', () => {
  it('aceita 1, 2 e 3, como número e como texto', () => {
    expect(validarBusca({ meses: 1 }).meses).toBe(1)
    expect(validarBusca({ meses: 2 }).meses).toBe(2)
    expect(validarBusca({ meses: 3 }).meses).toBe(3)
    // Digitado à mão ou vindo de link antigo, chega como texto.
    expect(validarBusca({ meses: '2' }).meses).toBe(2)
  })

  it('normaliza sempre para NÚMERO, nunca para o texto que veio', () => {
    expect(validarBusca({ meses: '3' }).meses).toBe(3)
    expect(typeof validarBusca({ meses: '3' }).meses).toBe('number')
  })

  it('o que não está na allowlist some, em vez de virar 400 no servidor', () => {
    for (const ruim of [0, 4, 9, -1, 2.5, '', 'tres', 'banana', true, null, [1, 2], { a: 1 }]) {
      expect(validarBusca({ meses: ruim })).toEqual({})
    }
  })

  it('atravessa a mudança de busca e some quando apagada', () => {
    expect(aplicarNaBusca({ mes: '2026-09' }, { meses: 1 })).toEqual({ mes: '2026-09', meses: 1 })
    // Voltar ao padrão (3) apaga a chave: a URL canônica não escreve o padrão.
    const proxima = aplicarNaBusca({ mes: '2026-09', meses: 1 }, { meses: undefined })
    expect(proxima).toEqual({ mes: '2026-09' })
    expect('meses' in proxima).toBe(false)
  })

  it('tamanhoDaJanelaValido é a MESMA porta que a tela usa', () => {
    expect(tamanhoDaJanelaValido('2')).toBe(2)
    expect(tamanhoDaJanelaValido(3)).toBe(3)
    expect(tamanhoDaJanelaValido('4')).toBeUndefined()
    expect(tamanhoDaJanelaValido('todos')).toBeUndefined()
  })
})
